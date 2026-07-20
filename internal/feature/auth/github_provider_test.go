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

// newTestGitHubProvider はhttptestサーバーに向けたGitHubProviderを生成するテスト用ヘルパーです。
// トークンエンドポイント・emails/userエンドポイントをすべてmuxで差し替えます。
func newTestGitHubProvider(t *testing.T, mux *http.ServeMux) *GitHubProvider {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p := NewGitHubProvider("cid", "secret", "http://localhost/cb", srv.Client())
	p.cfg.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/auth",
		TokenURL: srv.URL + "/token",
	}
	p.emailsURL = srv.URL + "/user/emails"
	p.userURL = srv.URL + "/user"
	return p
}

func TestGitHubProvider_AuthorizationURL(t *testing.T) {
	t.Parallel()

	p := newTestGitHubProvider(t, http.NewServeMux())

	authURL := p.AuthorizationURL("test-state", "unused-challenge")

	u, err := url.Parse(authURL)
	require.NoError(t, err)

	q := u.Query()
	assert.Equal(t, "test-state", q.Get("state"))
	assert.Equal(t, "user:email", q.Get("scope"))
	// GitHub の OAuth App は PKCE をサポートしないため code_challenge は付与されない。
	assert.False(t, q.Has("code_challenge"))
}

func TestGitHubProvider_ExchangeCode(t *testing.T) {
	t.Parallel()

	t.Run("success: picks primary+verified email and returns numeric user ID", func(t *testing.T) {
		t.Parallel()

		var emailsAccept, emailsAPIVersion, userAccept, userAPIVersion string

		mux := http.NewServeMux()
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
		})
		mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
			emailsAccept = r.Header.Get("Accept")
			emailsAPIVersion = r.Header.Get("X-GitHub-Api-Version")
			// primary+verified なエントリを先頭以外に配置し、選択ロジックを検証する。
			writeJSON(w, http.StatusOK, `[
				{"email":"other@example.com","primary":false,"verified":true},
				{"email":"unverified@example.com","primary":true,"verified":false},
				{"email":"gh@example.com","primary":true,"verified":true}
			]`)
		})
		mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
			userAccept = r.Header.Get("Accept")
			userAPIVersion = r.Header.Get("X-GitHub-Api-Version")
			writeJSON(w, http.StatusOK, `{"id":12345}`)
		})

		p := newTestGitHubProvider(t, mux)

		info, err := p.ExchangeCode(context.Background(), "auth-code", "")

		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "12345", info.ProviderUID)
		assert.Equal(t, "gh@example.com", info.Email)
		assert.Equal(t, "application/vnd.github+json", emailsAccept)
		assert.Equal(t, "2022-11-28", emailsAPIVersion)
		assert.Equal(t, "application/vnd.github+json", userAccept)
		assert.Equal(t, "2022-11-28", userAPIVersion)
	})

	tests := []struct {
		name          string
		tokenHandler  http.HandlerFunc
		emailsHandler http.HandlerFunc
		userHandler   http.HandlerFunc
		wantErrSubstr string
		wantErrIs     error
	}{
		{
			name: "error: token endpoint returns 400",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusBadRequest, `{"error":"invalid_grant"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `[]`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"id":1}`)
			},
			wantErrSubstr: "code exchange failed",
		},
		{
			name: "error: emails API returns 500",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusInternalServerError, `{"error":"boom"}`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"id":1}`)
			},
			wantErrSubstr: "emails API returned 500",
		},
		{
			name: "error: emails API returns invalid JSON",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `not-json`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"id":1}`)
			},
			wantErrSubstr: "failed to parse emails",
		},
		{
			name: "error: no primary verified email",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `[{"email":"a@example.com","primary":false,"verified":true},{"email":"b@example.com","primary":true,"verified":false}]`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"id":1}`)
			},
			wantErrIs: ErrOAuthEmailUnavailable,
		},
		{
			name: "error: user API returns 500",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `[{"email":"gh@example.com","primary":true,"verified":true}]`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusInternalServerError, `{"error":"boom"}`)
			},
			wantErrSubstr: "user API returned 500",
		},
		{
			name: "error: user API returns invalid ID",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `[{"email":"gh@example.com","primary":true,"verified":true}]`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"id":0}`)
			},
			wantErrSubstr: "invalid ID",
		},
		{
			name: "error: user API returns invalid JSON",
			tokenHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `{"access_token":"test-token","token_type":"Bearer"}`)
			},
			emailsHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `[{"email":"gh@example.com","primary":true,"verified":true}]`)
			},
			userHandler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, `not-json`)
			},
			wantErrSubstr: "failed to parse user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("/token", tt.tokenHandler)
			mux.HandleFunc("/user/emails", tt.emailsHandler)
			mux.HandleFunc("/user", tt.userHandler)

			p := newTestGitHubProvider(t, mux)

			info, err := p.ExchangeCode(context.Background(), "auth-code", "")

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
