package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// newTestGoogleProvider はhttptestサーバーに向けたGoogleProviderを生成するテスト用ヘルパーです。
// トークンエンドポイント・userinfoエンドポイントをすべてmuxで差し替えます。
func newTestGoogleProvider(t *testing.T, mux *http.ServeMux) *GoogleProvider {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p := NewGoogleProvider("cid", "secret", "http://localhost/cb", srv.Client())
	p.cfg.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/auth",
		TokenURL: srv.URL + "/token",
	}
	p.userinfoURL = srv.URL + "/userinfo"
	return p
}

// writeJSON はテスト用ハンドラーからJSONレスポンスを返す簡易ヘルパーです。
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func TestGoogleProvider_AuthorizationURL(t *testing.T) {
	t.Parallel()

	p := newTestGoogleProvider(t, http.NewServeMux())

	authURL := p.AuthorizationURL("test-state", "test-challenge")

	u, err := url.Parse(authURL)
	require.NoError(t, err)

	q := u.Query()
	assert.Equal(t, "test-state", q.Get("state"))
	assert.Equal(t, "test-challenge", q.Get("code_challenge"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "cid", q.Get("client_id"))
}

func TestGoogleProvider_ExchangeCode(t *testing.T) {
	t.Parallel()

	t.Run("success: returns user info and forwards code/verifier/token correctly", func(t *testing.T) {
		t.Parallel()

		var gotCode, gotVerifier, gotAuthHeader string

		mux := http.NewServeMux()
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotCode = r.FormValue("code")
			gotVerifier = r.FormValue("code_verifier")
			writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
		})
		mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
			gotAuthHeader = r.Header.Get("Authorization")
			writeJSON(w, http.StatusOK, `{"sub":"user-123","email":"user@example.com","email_verified":true}`)
		})

		p := newTestGoogleProvider(t, mux)

		info, err := p.ExchangeCode(context.Background(), "auth-code", "verifier-abc")

		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "user-123", info.ProviderUID)
		assert.Equal(t, "user@example.com", info.Email)
		assert.Equal(t, "auth-code", gotCode)
		assert.Equal(t, "verifier-abc", gotVerifier)
		assert.Equal(t, "Bearer test-token", gotAuthHeader)
	})

	tests := []struct {
		name            string
		tokenHandler    http.HandlerFunc
		userinfoHandler http.HandlerFunc
		wantErrSubstr   string
		wantErrIs       error
	}{
		{
			name: "error: token endpoint returns 400",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusBadRequest, `{"error":"invalid_grant"}`)
			},
			userinfoHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{}`)
			},
			wantErrSubstr: "code exchange failed",
		},
		{
			name: "error: userinfo endpoint returns 500",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			userinfoHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusInternalServerError, `{"error":"boom"}`)
			},
			wantErrSubstr: "userinfo API returned 500",
		},
		{
			name: "error: userinfo returns invalid JSON",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			userinfoHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `not-json`)
			},
			wantErrSubstr: "failed to parse userinfo",
		},
		{
			name: "error: email not verified",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			userinfoHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"sub":"s","email":"e@example.com","email_verified":false}`)
			},
			wantErrIs: ErrOAuthEmailUnavailable,
		},
		{
			name: "error: email verified but empty",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			userinfoHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"sub":"s","email":"","email_verified":true}`)
			},
			wantErrIs: ErrOAuthEmailUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("/token", tt.tokenHandler)
			mux.HandleFunc("/userinfo", tt.userinfoHandler)

			p := newTestGoogleProvider(t, mux)

			info, err := p.ExchangeCode(context.Background(), "auth-code", "verifier-abc")

			require.Error(t, err)
			assert.Nil(t, info)
			if tt.wantErrSubstr != "" {
				assert.ErrorContains(t, err, tt.wantErrSubstr)
			}
			if tt.wantErrIs != nil {
				assert.ErrorIs(t, err, tt.wantErrIs)
			}
		})
	}
}
