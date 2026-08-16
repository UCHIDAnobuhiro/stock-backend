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
	HandleCallbackFunc func(ctx context.Context, provider, code, state string) (auth.TokenPair, error)
}

func (m *mockOAuthUsecase) BeginAuth(ctx context.Context, provider string) (string, string, error) {
	return m.BeginAuthFunc(ctx, provider)
}

func (m *mockOAuthUsecase) HandleCallback(ctx context.Context, provider, code, state string) (auth.TokenPair, error) {
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
	return findCookieMatching(w, name, func(string) bool { return true })
}

func findCookieMatching(w *httptest.ResponseRecorder, name string, matches func(string) bool) string {
	for _, c := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, name+"=") && matches(c) {
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
	h := authhttp.NewOAuthHandler(uc, authhttp.SessionCookieConfig{
		Secure: true,
		Domain: "stockviewapp.com",
	}, "http://localhost:3000")

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
	assert.Contains(t, stateCookie, "Secure", "oauth_state should have Secure attribute")
	assert.NotContains(t, stateCookie, "Domain=", "oauth_state must remain host-only")
}

// TestOAuthHandler_RedirectRateLimitError は OAuth のレートリミット拒否が
// 生の JSON ではなく、理由を識別できるログイン画面へリダイレクトされることを検証します。
func TestOAuthHandler_RedirectRateLimitError(t *testing.T) {
	t.Parallel()

	const frontendURL = "http://localhost:3000"
	tests := []struct {
		name             string
		status           int
		expectedLocation string
	}{
		{
			name:             "rate limited",
			status:           http.StatusTooManyRequests,
			expectedLocation: frontendURL + "/login?error=rate_limited",
		},
		{
			name:             "rate limiter unavailable",
			status:           http.StatusServiceUnavailable,
			expectedLocation: frontendURL + "/login?error=service_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := authhttp.NewOAuthHandler(nil, authhttp.SessionCookieConfig{}, frontendURL)
			req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google", nil)
			w := httptest.NewRecorder()

			h.RedirectRateLimitError(w, req, tt.status)

			assert.Equal(t, http.StatusFound, w.Code)
			assert.Equal(t, tt.expectedLocation, w.Header().Get("Location"))
		})
	}
}

// TestOAuthHandler_Callback_StateBinding はコールバック時の state Cookie 照合
// （ログイン CSRF 対策）を検証します。
func TestOAuthHandler_Callback_StateBinding(t *testing.T) {
	t.Parallel()

	const frontendURL = "http://localhost:3000"

	tests := []struct {
		name             string
		query            string
		stateCookie      string // 空文字なら Cookie を付与しない
		expectedStatus   int
		expectedLocation string
		callbackCalled   bool
	}{
		{
			name:             "success: query state matches cookie",
			query:            "?code=auth-code&state=abc",
			stateCookie:      "abc",
			expectedStatus:   http.StatusFound,
			expectedLocation: frontendURL,
			callbackCalled:   true,
		},
		{
			name:             "failure: no state cookie",
			query:            "?code=auth-code&state=abc",
			stateCookie:      "",
			expectedStatus:   http.StatusFound,
			expectedLocation: frontendURL + "/login?error=oauth_failed",
			callbackCalled:   false,
		},
		{
			name:             "failure: cookie mismatch (login CSRF attempt)",
			query:            "?code=auth-code&state=attacker-state",
			stateCookie:      "victim-state",
			expectedStatus:   http.StatusFound,
			expectedLocation: frontendURL + "/login?error=oauth_failed",
			callbackCalled:   false,
		},
		{
			name:             "failure: missing code/state query",
			query:            "",
			stateCookie:      "",
			expectedStatus:   http.StatusFound,
			expectedLocation: frontendURL + "/login?error=oauth_failed",
			callbackCalled:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			uc := &mockOAuthUsecase{
				HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (auth.TokenPair, error) {
					called = true
					return auth.TokenPair{AccessToken: "dummy-jwt-token", RefreshToken: "dummy-refresh-token"}, nil
				},
			}
			cookies := authhttp.SessionCookieConfig{Secure: true, Domain: "stockviewapp.com"}
			h := authhttp.NewOAuthHandler(uc, cookies, frontendURL)

			req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback"+tt.query, nil)
			if tt.stateCookie != "" {
				req.AddCookie(&http.Cookie{Name: "oauth_state", Value: tt.stateCookie})
			}
			w := httptest.NewRecorder()
			newOAuthRouter(h).ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Equal(t, tt.expectedLocation, w.Header().Get("Location"))
			assert.Equal(t, tt.callbackCalled, called, "HandleCallback の呼び出し有無")

			// いずれのケースでも state Cookie は削除される（使い捨て）。
			if tt.stateCookie != "" {
				stateCookie := findCookie(w, "oauth_state")
				if stateCookie != "" {
					assert.Contains(t, stateCookie, "Max-Age=0", "oauth_state should be cleared")
					assert.NotContains(t, stateCookie, "Domain=", "oauth_state deletion must remain host-only")
				}
			}

			// 成功時は認証Cookieがセットされ、失敗時はセットされないこと。
			if tt.callbackCalled {
				for _, name := range []string{"auth_token", "refresh_token", "csrf_token"} {
					issued := findCookieMatching(w, name, func(cookie string) bool {
						return !strings.Contains(cookie, "Max-Age=0")
					})
					assert.Contains(t, issued, "Domain="+cookies.Domain, name+" should use the configured parent domain")
					legacyDeletion := findCookieMatching(w, name, func(cookie string) bool {
						return strings.Contains(cookie, "Max-Age=0") && !strings.Contains(cookie, "Domain=")
					})
					assert.NotEmpty(t, legacyDeletion, name+" legacy host-only cookie should be deleted")
				}
			} else {
				assert.Empty(t, findCookie(w, "auth_token"), "auth_token must not be set on failure")
				assert.Empty(t, findCookie(w, "refresh_token"), "refresh_token must not be set on failure")
				assert.Empty(t, findCookie(w, "csrf_token"), "csrf_token must not be set on failure")
			}
		})
	}
}

// TestOAuthHandler_Callback_StateNotFound はサーバ側 state（Redis）の照合失敗時に
// フロントエンドのログイン画面へ error=oauth_failed でリダイレクトすることを検証します。
func TestOAuthHandler_Callback_StateNotFound(t *testing.T) {
	t.Parallel()

	const frontendURL = "http://localhost:3000"
	uc := &mockOAuthUsecase{
		HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (auth.TokenPair, error) {
			return auth.TokenPair{}, auth.ErrStateNotFound
		},
	}
	h := authhttp.NewOAuthHandler(uc, authhttp.SessionCookieConfig{}, frontendURL)

	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback?code=auth-code&state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()
	newOAuthRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, frontendURL+"/login?error=oauth_failed", w.Header().Get("Location"))
	assert.Empty(t, findCookie(w, "auth_token"), "auth_token must not be set on failure")
	assert.Empty(t, findCookie(w, "csrf_token"), "csrf_token must not be set on failure")
}

// TestOAuthHandler_Callback_EmailConflict は同メールの既存アカウントが存在し
// 自動リンクが拒否された場合に、フロントエンドのログイン画面へ error=account_conflict で
// リダイレクトし、認証 Cookie を設定しないことを検証します。
func TestOAuthHandler_Callback_EmailConflict(t *testing.T) {
	t.Parallel()

	const frontendURL = "http://localhost:3000"
	uc := &mockOAuthUsecase{
		HandleCallbackFunc: func(ctx context.Context, provider, code, state string) (auth.TokenPair, error) {
			return auth.TokenPair{}, auth.ErrOAuthEmailConflict
		},
	}
	h := authhttp.NewOAuthHandler(uc, authhttp.SessionCookieConfig{}, frontendURL)

	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/google/callback?code=auth-code&state=abc", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "abc"})
	w := httptest.NewRecorder()
	newOAuthRouter(h).ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, frontendURL+"/login?error=account_conflict", w.Header().Get("Location"))
	assert.Empty(t, findCookie(w, "auth_token"), "auth_token must not be set on conflict")
	assert.Empty(t, findCookie(w, "csrf_token"), "csrf_token must not be set on conflict")
}
