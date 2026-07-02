package authhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
)

// mockOAuthUsecase は authhttp.OAuthUsecase インターフェースのモック実装です。
type mockOAuthUsecase struct {
	BeginAuthFunc      func(ctx context.Context, provider string) (string, string, error)
	HandleCallbackFunc func(ctx context.Context, provider, code, state string) (string, error)
}

func (m *mockOAuthUsecase) BeginAuth(ctx context.Context, provider string) (string, string, error) {
	return m.BeginAuthFunc(ctx, provider)
}

func (m *mockOAuthUsecase) HandleCallback(ctx context.Context, provider, code, state string) (string, error) {
	return m.HandleCallbackFunc(ctx, provider, code, state)
}

// newOAuthRouter は provider URLパラメータを解決するための chi ルーターを返します。
func newOAuthRouter(h *authhttp.OAuthHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/auth/oauth/{provider}", h.BeginAuth)
	r.Get("/auth/oauth/{provider}/callback", h.Callback)
	return r
}

// findCookie は Set-Cookie ヘッダーから指定名の Cookie 文字列を返します。
func findCookie(w *httptest.ResponseRecorder, name string) string {
	for _, c := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, name+"=") {
			return c
		}
	}
	return ""
}

// TestOAuthHandler_BeginAuth_SetsStateCookie は BeginAuth が
// HttpOnly / SameSite=Lax の state Cookie を設定し、認可URLへリダイレクトすることを検証します。
func TestOAuthHandler_BeginAuth_SetsStateCookie(t *testing.T) {
	t.Parallel()

	uc := &mockOAuthUsecase{
		BeginAuthFunc: func(ctx context.Context, provider string) (string, string, error) {
			return "https://provider.example.com/authorize?state=abc", "abc", nil
		},
	}
	h := authhttp.NewOAuthHandler(uc, false, "http://localhost:3000")

	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google", nil)
	w := httptest.NewRecorder()
	newOAuthRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "https://provider.example.com/authorize?state=abc", w.Header().Get("Location"))

	stateCookie := findCookie(w, "oauth_state")
	require.NotEmpty(t, stateCookie, "oauth_state cookie should be set")
	assert.Contains(t, stateCookie, "oauth_state=abc")
	assert.Contains(t, stateCookie, "HttpOnly", "oauth_state should be HttpOnly")
	assert.Contains(t, stateCookie, "SameSite=Lax", "oauth_state should have SameSite=Lax")
}

// TestOAuthHandler_Callback_StateBinding はコールバック時の state Cookie 照合
// （ログイン CSRF 対策）を検証します。
func TestOAuthHandler_Callback_StateBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		query          string
		stateCookie    string // 空文字なら Cookie を付与しない
		expectedStatus int
		callbackCalled bool
	}{
		{
			name:           "success: query state matches cookie",
			query:          "?code=auth-code&state=abc",
			stateCookie:    "abc",
			expectedStatus: http.StatusFound,
			callbackCalled: true,
		},
		{
			name:           "failure: no state cookie",
			query:          "?code=auth-code&state=abc",
			stateCookie:    "",
			expectedStatus: http.StatusBadRequest,
			callbackCalled: false,
		},
		{
			name:           "failure: cookie mismatch (login CSRF attempt)",
			query:          "?code=auth-code&state=attacker-state",
			stateCookie:    "victim-state",
			expectedStatus: http.StatusBadRequest,
			callbackCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			uc := &mockOAuthUsecase{
				HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (string, error) {
					called = true
					return "dummy-jwt-token", nil
				},
			}
			h := authhttp.NewOAuthHandler(uc, false, "http://localhost:3000")

			req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback"+tt.query, nil)
			if tt.stateCookie != "" {
				req.AddCookie(&http.Cookie{Name: "oauth_state", Value: tt.stateCookie})
			}
			w := httptest.NewRecorder()
			newOAuthRouter(h).ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Equal(t, tt.callbackCalled, called, "HandleCallback の呼び出し有無")

			// いずれのケースでも state Cookie は削除される（使い捨て）。
			if tt.stateCookie != "" || tt.expectedStatus == http.StatusBadRequest {
				stateCookie := findCookie(w, "oauth_state")
				if stateCookie != "" {
					assert.Contains(t, stateCookie, "Max-Age=0", "oauth_state should be cleared")
				}
			}

			// 成功時は auth_token / csrf_token がセットされること。
			if tt.expectedStatus == http.StatusFound {
				assert.NotEmpty(t, findCookie(w, "auth_token"), "auth_token should be set on success")
				assert.NotEmpty(t, findCookie(w, "csrf_token"), "csrf_token should be set on success")
			}
		})
	}
}

// TestOAuthHandler_Callback_StateNotFound はサーバ側 state（Redis）の照合失敗時に
// 400 を返すことを検証します。
func TestOAuthHandler_Callback_StateNotFound(t *testing.T) {
	t.Parallel()

	uc := &mockOAuthUsecase{
		HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (string, error) {
			return "", auth.ErrStateNotFound
		},
	}
	h := authhttp.NewOAuthHandler(uc, false, "http://localhost:3000")

	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback?code=auth-code&state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()
	newOAuthRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestOAuthHandler_Callback_EmailConflict は同メールの既存アカウントが存在し
// 自動リンクが拒否された場合に 409 を返し、認証 Cookie を設定しないことを検証します。
func TestOAuthHandler_Callback_EmailConflict(t *testing.T) {
	t.Parallel()

	uc := &mockOAuthUsecase{
		HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (string, error) {
			return "", auth.ErrOAuthEmailConflict
		},
	}
	h := authhttp.NewOAuthHandler(uc, false, "http://localhost:3000")

	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback?code=auth-code&state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()
	newOAuthRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "email already registered with a different login method")
	assert.Empty(t, findCookie(w, "auth_token"), "auth_token must not be set on conflict")
	assert.Empty(t, findCookie(w, "csrf_token"), "csrf_token must not be set on conflict")
}
