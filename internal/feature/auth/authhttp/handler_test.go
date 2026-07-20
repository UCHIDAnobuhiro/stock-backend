package authhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// H は JSON ボディ構築用の簡易マップ型です。
type H = map[string]any

// mockUsecase はUsecaseインターフェースのモック実装です。
type mockUsecase struct {
	SignupFunc func(ctx context.Context, email, password string) (int64, error)
	LoginFunc  func(ctx context.Context, email, password string) (string, error)
}

// Signup はSignupメソッドのモック実装です。
func (m *mockUsecase) Signup(ctx context.Context, email, password string) (int64, error) {
	if m.SignupFunc != nil {
		return m.SignupFunc(ctx, email, password)
	}
	return 1, nil // デフォルト: 成功
}

// Login はLoginメソッドのモック実装です。
func (m *mockUsecase) Login(ctx context.Context, email, password string) (string, error) {
	if m.LoginFunc != nil {
		return m.LoginFunc(ctx, email, password)
	}
	return "", errors.New("login failed") // デフォルト: 失敗
}

// mockPostHook は auth.UserCreatedHook インターフェースのモック実装です。
type mockPostHook struct {
	OnUserCreatedFunc func(ctx context.Context, userID int64) error
	called            bool
}

// OnUserCreated はサインアップ後フックのモック実装です。
func (m *mockPostHook) OnUserCreated(ctx context.Context, userID int64) error {
	m.called = true
	if m.OnUserCreatedFunc != nil {
		return m.OnUserCreatedFunc(ctx, userID)
	}
	return nil
}

// makeRequest はHTTPリクエストを作成し、指定ハンドラーを直接実行するヘルパー関数です。
func makeRequest(t *testing.T, handler http.HandlerFunc, method, path string, body H) *httptest.ResponseRecorder {
	t.Helper()

	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler(w, req)

	return w
}

// assertJSONResponse はJSONレスポンスのステータスコードとボディを検証するヘルパー関数です。
func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int, expectedBody H) {
	t.Helper()

	assert.Equal(t, expectedStatus, w.Code)

	var responseBody H
	err := json.Unmarshal(w.Body.Bytes(), &responseBody)
	require.NoError(t, err)

	assert.Equal(t, expectedBody, responseBody)
}

// assertLoginCookies はログイン成功時のSet-CookieヘッダーにCookieが正しく設定されていることを検証します。
// secureCookie=true の場合は Secure 属性も検証します。
func assertLoginCookies(t *testing.T, w *httptest.ResponseRecorder, secureCookie bool) {
	t.Helper()

	var authTokenCookie, csrfTokenCookie string
	for _, c := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, "auth_token=") {
			authTokenCookie = c
		}
		if strings.HasPrefix(c, "csrf_token=") {
			csrfTokenCookie = c
		}
	}

	// auth_token: HttpOnly かつ SameSite=Lax であること
	assert.NotEmpty(t, authTokenCookie, "auth_token cookie should be set")
	assert.Contains(t, authTokenCookie, "HttpOnly", "auth_token should be HttpOnly")
	assert.Contains(t, authTokenCookie, "SameSite=Lax", "auth_token should have SameSite=Lax")

	// csrf_token: 非HttpOnly（JavaScriptから読み取れる）かつ SameSite=Lax であること
	assert.NotEmpty(t, csrfTokenCookie, "csrf_token cookie should be set")
	assert.NotContains(t, csrfTokenCookie, "HttpOnly", "csrf_token must not be HttpOnly")
	assert.Contains(t, csrfTokenCookie, "SameSite=Lax", "csrf_token should have SameSite=Lax")

	// secureCookie=true の場合: 両Cookieに Secure 属性が付くこと / false の場合: 付かないこと
	if secureCookie {
		assert.Contains(t, authTokenCookie, "Secure", "auth_token should have Secure attribute")
		assert.Contains(t, csrfTokenCookie, "Secure", "csrf_token should have Secure attribute")
	} else {
		assert.NotContains(t, authTokenCookie, "Secure", "auth_token must not have Secure attribute")
		assert.NotContains(t, csrfTokenCookie, "Secure", "csrf_token must not have Secure attribute")
	}
}

// TestAuthHandler_Signup はサインアップハンドラーのHTTPリクエスト/レスポンス処理をテストします。
func TestAuthHandler_Signup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		requestBody    H
		mockSignupFunc func(ctx context.Context, email, password string) (int64, error)
		expectedStatus int
		expectedBody   H
	}{
		{
			name:           "success: user registration",
			requestBody:    H{"email": "test@example.com", "password": "password12345"},
			mockSignupFunc: func(ctx context.Context, email, password string) (int64, error) { return 1, nil },
			expectedStatus: http.StatusCreated,
			expectedBody:   H{"message": "ok"},
		},
		// 注: email 形式・password 長さ等のスキーマバリデーションは
		// OpenAPI バリデーションミドルウェア（internal/transport/openapivalidate）の
		// 責務に移行したため、その検証は middleware_test.go で実施する。
		{
			name:        "failure: duplicate email (usecase error)",
			requestBody: H{"email": "existing@example.com", "password": "password12345"},
			mockSignupFunc: func(ctx context.Context, email, password string) (int64, error) {
				return 0, errors.New("email already exists")
			},
			expectedStatus: http.StatusConflict,
			expectedBody:   H{"error": "signup failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{SignupFunc: tt.mockSignupFunc}
			h := authhttp.NewHandler(mockUC, nil, false, "", nil)

			w := makeRequest(t, h.Signup, http.MethodPost, "/signup", tt.requestBody)
			assertJSONResponse(t, w, tt.expectedStatus, tt.expectedBody)
		})
	}
}

// TestAuthHandler_Signup_HookFailureIsNonFatal はサインアップ後フックが失敗しても
// ユーザー作成が成功している限り 201 を返す（非致命的扱い）ことを検証します（issue #196）。
func TestAuthHandler_Signup_HookFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	mockUC := &mockUsecase{
		SignupFunc: func(ctx context.Context, email, password string) (int64, error) { return 1, nil },
	}
	hook := &mockPostHook{
		OnUserCreatedFunc: func(ctx context.Context, userID int64) error {
			return errors.New("watchlist init failed")
		},
	}
	h := authhttp.NewHandler(mockUC, nil, false, "", nil, hook)

	w := makeRequest(t, h.Signup, http.MethodPost, "/signup", H{
		"email":    "test@example.com",
		"password": "password12345",
	})

	assertJSONResponse(t, w, http.StatusCreated, H{"message": "ok"})
	assert.True(t, hook.called, "後処理フックが呼ばれること")
}

// TestAuthHandler_Login_RateLimited はメールベースのレートリミット超過時に429が返されることを検証します。
func TestAuthHandler_Login_RateLimited(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	// Luaスクリプトモック: allowed=0（レートリミット超過）を返す
	match := mock.CustomMatch(func(expected, actual []interface{}) error {
		return nil
	})
	key := "rl:login:email:test@example.com"
	httpratelimit.ExpectAllow(match, key, false, 5)

	limiter := httpratelimit.NewLimiter(rdb)
	loginCalled := false
	mockUC := &mockUsecase{
		LoginFunc: func(ctx context.Context, email, password string) (string, error) {
			loginCalled = true
			return "", errors.New("should not be called")
		},
	}
	h := authhttp.NewHandler(mockUC, limiter, false, "", nil)

	w := makeRequest(t, h.Login, http.MethodPost, "/login", H{
		"email":    "test@example.com",
		"password": "password12345",
	})

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "900", w.Header().Get("Retry-After"))

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "too many requests", body["error"])

	assert.False(t, loginCalled, "レートリミット超過時はUsecaseが呼ばれないこと")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAuthHandler_Login_RateLimiterUnavailable はメールベースのレートリミット判定がRedis障害で
// 失敗した場合、fail-closed方針（issue #266）により503が返り、Usecaseが呼ばれないことを検証します。
func TestAuthHandler_Login_RateLimiterUnavailable(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	// Luaスクリプトモック: Redis障害（EvalShaエラー）を再現する
	match := mock.CustomMatch(func(expected, actual []interface{}) error {
		return nil
	})
	key := "rl:login:email:test@example.com"
	httpratelimit.ExpectAllowError(match, key, errors.New("connection refused"))

	limiter := httpratelimit.NewLimiter(rdb)
	loginCalled := false
	mockUC := &mockUsecase{
		LoginFunc: func(ctx context.Context, email, password string) (string, error) {
			loginCalled = true
			return "", errors.New("should not be called")
		},
	}
	h := authhttp.NewHandler(mockUC, limiter, false, "", nil)

	w := makeRequest(t, h.Login, http.MethodPost, "/login", H{
		"email":    "test@example.com",
		"password": "password12345",
	})

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Empty(t, w.Header().Get("Retry-After"), "Retry-Afterヘッダーは付与しない")

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "service temporarily unavailable", body["error"])

	assert.False(t, loginCalled, "レートリミッター障害時はUsecaseが呼ばれないこと")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// newAllowingLimiter はメールベースのレートリミット（rl:login:email:<email>）を常に許可する
// モックLimiterを生成します。Loginハンドラーはメールベースレートリミットにfail-closed
// （issue #266）を用いるため、nilを渡すと503（ServiceUnavailable）になってしまいます。
// レートリミット判定そのものを検証しないテストではこのヘルパーで許可済みのLimiterを用意します。
func newAllowingLimiter(t *testing.T, email string) *httpratelimit.Limiter {
	t.Helper()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	match := mock.CustomMatch(func(expected, actual []interface{}) error {
		return nil
	})
	key := fmt.Sprintf("rl:login:email:%s", auth.NormalizeEmail(email))
	httpratelimit.ExpectAllow(match, key, true, 0)

	return httpratelimit.NewLimiter(rdb)
}

// TestAuthHandler_Login はログインハンドラーのHTTPリクエスト/レスポンス処理をテストします。
func TestAuthHandler_Login(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		requestBody    H
		mockLoginFunc  func(ctx context.Context, email, password string) (string, error)
		expectedStatus int
		expectedBody   H
		checkCookies   bool
		secureCookie   bool
	}{
		{
			name:           "success: user login",
			requestBody:    H{"email": "test@example.com", "password": "password12345"},
			mockLoginFunc:  func(ctx context.Context, email, password string) (string, error) { return "dummy-jwt-token", nil },
			expectedStatus: http.StatusOK,
			expectedBody:   H{"message": "ok"},
			checkCookies:   true,
			secureCookie:   false,
		},
		{
			name:           "success: user login (secureCookie=true)",
			requestBody:    H{"email": "test@example.com", "password": "password12345"},
			mockLoginFunc:  func(ctx context.Context, email, password string) (string, error) { return "dummy-jwt-token", nil },
			expectedStatus: http.StatusOK,
			expectedBody:   H{"message": "ok"},
			checkCookies:   true,
			secureCookie:   true,
		},
		// 注: email 形式・必須項目等のスキーマバリデーションは OpenAPI バリデーション
		// ミドルウェアの責務に移行したため、その検証は middleware_test.go で実施する。
		{
			name:        "failure: invalid credentials (usecase error)",
			requestBody: H{"email": "wrong@example.com", "password": "wrong-password"},
			mockLoginFunc: func(ctx context.Context, email, password string) (string, error) {
				return "", errors.New("invalid email or password")
			},
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   H{"error": "invalid email or password"},
		},
		{
			name:        "failure: JWT secret not set (usecase error)",
			requestBody: H{"email": "test@example.com", "password": "password12345"},
			mockLoginFunc: func(ctx context.Context, email, password string) (string, error) {
				return "", errors.New("server misconfigured: JWT_SECRET missing")
			},
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   H{"error": "invalid email or password"}, // Usecaseのエラーメッセージは隠蔽される
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{LoginFunc: tt.mockLoginFunc}
			email, _ := tt.requestBody["email"].(string)
			limiter := newAllowingLimiter(t, email)
			h := authhttp.NewHandler(mockUC, limiter, tt.secureCookie, "", nil)

			w := makeRequest(t, h.Login, http.MethodPost, "/login", tt.requestBody)
			assertJSONResponse(t, w, tt.expectedStatus, tt.expectedBody)
			if tt.checkCookies {
				assertLoginCookies(t, w, tt.secureCookie)
			}
		})
	}
}

// TestAuthHandler_Logout はログアウトハンドラーがCookieを削除することを検証します。
func TestAuthHandler_Logout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		secureCookie bool
	}{
		{name: "secureCookie=false", secureCookie: false},
		{name: "secureCookie=true", secureCookie: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := authhttp.NewHandler(&mockUsecase{}, nil, tt.secureCookie, "", nil)

			w := makeRequest(t, h.Logout, http.MethodDelete, "/logout", H{})

			assert.Equal(t, http.StatusOK, w.Code)

			var authTokenCookie, csrfTokenCookie string
			for _, c := range w.Header().Values("Set-Cookie") {
				if strings.HasPrefix(c, "auth_token=") {
					authTokenCookie = c
				}
				if strings.HasPrefix(c, "csrf_token=") {
					csrfTokenCookie = c
				}
			}

			// ログアウト時は Max-Age=0 でCookieを削除すること
			assert.NotEmpty(t, authTokenCookie, "auth_token cookie should be present in response")
			assert.Contains(t, authTokenCookie, "Max-Age=0", "auth_token cookie should be deleted (Max-Age=0)")

			assert.NotEmpty(t, csrfTokenCookie, "csrf_token cookie should be present in response")
			assert.Contains(t, csrfTokenCookie, "Max-Age=0", "csrf_token cookie should be deleted (Max-Age=0)")

			// secureCookie=true の場合: 両Cookieに Secure 属性が付くこと / false の場合: 付かないこと
			if tt.secureCookie {
				assert.Contains(t, authTokenCookie, "Secure", "auth_token should have Secure attribute")
				assert.Contains(t, csrfTokenCookie, "Secure", "csrf_token should have Secure attribute")
			} else {
				assert.NotContains(t, authTokenCookie, "Secure", "auth_token must not have Secure attribute")
				assert.NotContains(t, csrfTokenCookie, "Secure", "csrf_token must not have Secure attribute")
			}
		})
	}
}

// TestAuthHandler_Logout_RevokesToken はログアウト時にリクエストが保持するJWTがブラックリストへ
// 登録され、有効期限前でも即時失効することを検証します（issue #263）。
func TestAuthHandler_Logout_RevokesToken(t *testing.T) {
	t.Parallel()

	const secret = "logout-revoke-secret"
	gen := jwt.NewGenerator(secret, time.Hour)
	token, err := gen.GenerateToken(1, "test@example.com")
	require.NoError(t, err)

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	// ttlは処理にかかる時間だけ厳密一致しないため、失効登録キーの形式のみ検証する。
	match := mock.CustomMatch(func(_, actual []interface{}) error {
		if len(actual) < 2 {
			t.Fatalf("unexpected SET args: %+v", actual)
		}
		key, ok := actual[1].(string)
		if !ok || !strings.HasPrefix(key, "jwt:blacklist:") {
			t.Errorf("unexpected blacklist key: %v", actual[1])
		}
		return nil
	})
	match.ExpectSet("ignored", "1", time.Hour).SetVal("OK")

	blacklist := jwt.NewBlacklist(rdb)
	h := authhttp.NewHandler(&mockUsecase{}, nil, false, secret, blacklist)

	req := httptest.NewRequest(http.MethodDelete, "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.Logout(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
