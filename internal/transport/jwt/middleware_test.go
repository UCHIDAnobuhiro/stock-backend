package jwt

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	gojwt "github.com/golang-jwt/jwt/v5"
)

// runAuth はミドルウェアを実行し、レスポンスレコーダー・next が呼ばれたか・
// next が受け取ったリクエストを返すテストヘルパーです。
func runAuth(authHeader string, mutate func(r *http.Request)) (*httptest.ResponseRecorder, bool, *http.Request) {
	return runAuthWithBlacklist(authHeader, nil, mutate)
}

// runAuthWithBlacklist はブラックリストを指定できる runAuth のバリエーションです。
func runAuthWithBlacklist(authHeader string, blacklist *Blacklist, mutate func(r *http.Request)) (*httptest.ResponseRecorder, bool, *http.Request) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if mutate != nil {
		mutate(req)
	}

	var nextCalled bool
	var seen *http.Request
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nextCalled = true
		seen = r
	})
	// シークレットは起動時注入に変更されたが、既存テストの t.Setenv パターンを維持するため
	// ヘルパー内で env から読み取って注入する。
	AuthRequired(os.Getenv(EnvKeyJWTSecret), blacklist)(next).ServeHTTP(w, req)
	return w, nextCalled, seen
}

// TestAuthRequired_MissingBearerToken はBearerトークンがない場合やプレフィックスが不正な場合に401が返されることを検証します。
func TestAuthRequired_MissingBearerToken(t *testing.T) {
	t.Setenv(EnvKeyJWTSecret, "test-secret")

	tests := []struct {
		name       string
		authHeader string
	}{
		{"no header", ""},
		{"empty header", ""},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"bearer lowercase", "bearer token123"},
		{"no space after Bearer", "Bearertoken123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, nextCalled, _ := runAuth(tt.authHeader, nil)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
			}
			if nextCalled {
				t.Error("expected request to be aborted (next must not be called)")
			}
		})
	}
}

// TestAuthRequired_MissingJWTSecret はJWT_SECRET環境変数が未設定の場合に500が返されることを検証します。
func TestAuthRequired_MissingJWTSecret(t *testing.T) {
	t.Setenv(EnvKeyJWTSecret, "")

	w, nextCalled, _ := runAuth("Bearer sometoken", nil)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}
	if nextCalled {
		t.Error("expected request to be aborted")
	}
}

// TestAuthRequired_InvalidToken は不正なトークン（改ざん・期限切れ等）で401が返されることを検証します。
func TestAuthRequired_InvalidToken(t *testing.T) {
	const testSecret = "test-secret-key-for-invalid"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	tests := []struct {
		name  string
		token string
	}{
		{"malformed token", "not.a.valid.token"},
		{"random string", "randomstring"},
		{"wrong secret", createTokenWithSecret("wrong-secret", 1, time.Hour)},
		{"expired token", createTokenWithSecret(testSecret, 1, -time.Hour)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, _, _ := runAuth("Bearer "+tt.token, nil)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
			}
		})
	}
}

// TestAuthRequired_ValidToken は有効なトークンでリクエストが通過し、コンテキストにユーザーIDが設定されることを検証します。
func TestAuthRequired_ValidToken(t *testing.T) {
	const testSecret = "test-secret-key-for-valid"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	tests := []struct {
		name           string
		userID         int64
		expectedUserID int64
	}{
		{"user id 1", 1, 1},
		{"user id 42", 42, 42},
		{"max int64 user id", 9223372036854775807, 9223372036854775807},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := createTokenWithSecret(testSecret, tt.userID, time.Hour)

			w, nextCalled, seen := runAuth("Bearer "+token, nil)

			if !nextCalled {
				t.Errorf("expected request not to be aborted, response: %s", w.Body.String())
				return
			}

			userID, exists := UserIDFromContext(seen.Context())
			if !exists {
				t.Error("expected userID to be set in context")
				return
			}
			if userID != tt.expectedUserID {
				t.Errorf("expected userID %d, got %d", tt.expectedUserID, userID)
			}
		})
	}
}

// TestAuthRequired_LegacyNumericSubject は移行前の数値subjectが安全な範囲で受理されることを検証します。
func TestAuthRequired_LegacyNumericSubject(t *testing.T) {
	const testSecret = "test-secret-key-for-legacy"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	token := createLegacyTokenWithSecret(testSecret, 42, time.Hour)
	w, nextCalled, seen := runAuth("Bearer "+token, nil)

	if !nextCalled {
		t.Fatalf("expected request not to be aborted, response: %s", w.Body.String())
	}
	if userID, _ := UserIDFromContext(seen.Context()); userID != int64(42) {
		t.Errorf("expected userID 42, got %v", userID)
	}
}

// TestAuthRequired_InvalidSubject は不正なsubjectが拒否されることを検証します。
func TestAuthRequired_InvalidSubject(t *testing.T) {
	const testSecret = "test-secret-key-for-subject"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	tests := []struct {
		name string
		sub  any
	}{
		{"missing subject", nil},
		{"non-numeric string", "abc"},
		{"zero", "0"},
		{"unsafe legacy number", float64(1 << 53)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := createTokenWithSubject(testSecret, tt.sub, time.Hour)
			w, _, _ := runAuth("Bearer "+token, nil)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
			}
		})
	}
}

// TestAuthRequired_InvalidSigningMethod はnoneアルゴリズム（未署名）のトークンが拒否されることを検証します。
func TestAuthRequired_InvalidSigningMethod(t *testing.T) {
	const testSecret = "test-secret-key-for-signing"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	// "none" アルゴリズム（未署名）のトークンを生成
	token := gojwt.NewWithClaims(gojwt.SigningMethodNone, gojwt.MapClaims{
		"sub": float64(1),
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	tokenStr, _ := token.SignedString(gojwt.UnsafeAllowNoneSignatureType)

	w, _, _ := runAuth("Bearer "+tokenStr, nil)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

// TestAuthRequired_CookiePreferred はCookie認証が設定され、認証方式がcookieになることを検証します。
func TestAuthRequired_CookiePreferred(t *testing.T) {
	const testSecret = "test-secret-key-for-cookie"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	token := createTokenWithSecret(testSecret, 7, time.Hour)
	_, nextCalled, seen := runAuth("", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "auth_token", Value: token})
	})

	if !nextCalled {
		t.Fatal("expected request to pass")
	}
	if src := AuthSourceFromContext(seen.Context()); src != AuthSourceCookie {
		t.Errorf("expected auth source %q, got %q", AuthSourceCookie, src)
	}
}

// TestAuthRequired_RevokedToken はログアウトでブラックリストに登録済みのjtiを持つ
// トークンが401で拒否されることを検証します（issue #263: ログアウト即時失効）。
func TestAuthRequired_RevokedToken(t *testing.T) {
	const testSecret = "test-secret-key-for-revoked"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	const jti = "revoked-jti-123"
	mock.ExpectExists(blacklistKey(jti)).SetVal(1)

	token := createTokenWithJTI(testSecret, 1, time.Hour, jti)
	blacklist := NewBlacklist(rdb)

	w, nextCalled, _ := runAuthWithBlacklist("Bearer "+token, blacklist, nil)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
	if nextCalled {
		t.Error("expected request to be aborted (next must not be called)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestAuthRequired_NonRevokedToken はブラックリストに登録されていないトークンが
// 通常どおり通過することを検証します。
func TestAuthRequired_NonRevokedToken(t *testing.T) {
	const testSecret = "test-secret-key-for-non-revoked"
	t.Setenv(EnvKeyJWTSecret, testSecret)

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	const jti = "active-jti-456"
	mock.ExpectExists(blacklistKey(jti)).SetVal(0)

	token := createTokenWithJTI(testSecret, 1, time.Hour, jti)
	blacklist := NewBlacklist(rdb)

	w, nextCalled, _ := runAuthWithBlacklist("Bearer "+token, blacklist, nil)

	if nextCalled != true {
		t.Errorf("expected request to pass, response: %s", w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// createTokenWithSecret はテスト用に指定されたシークレットとユーザーIDで署名済みJWTトークンを生成します。
func createTokenWithSecret(secret string, userID int64, expiration time.Duration) string {
	return createTokenWithSubject(secret, strconv.FormatInt(userID, 10), expiration)
}

// createLegacyTokenWithSecret は移行前と同じ数値subjectのトークンを生成します。
func createLegacyTokenWithSecret(secret string, userID int64, expiration time.Duration) string {
	return createTokenWithSubject(secret, float64(userID), expiration)
}

// createTokenWithJTI はブラックリスト検証テスト用に、指定したjtiクレームを含む
// 署名済みJWTトークンを生成します。
func createTokenWithJTI(secret string, userID int64, expiration time.Duration, jti string) string {
	claims := gojwt.MapClaims{
		"sub":   strconv.FormatInt(userID, 10),
		"exp":   time.Now().Add(expiration).Unix(),
		"iat":   time.Now().Unix(),
		"email": "test@example.com",
		"jti":   jti,
	}
	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}

func createTokenWithSubject(secret string, subject any, expiration time.Duration) string {
	claims := gojwt.MapClaims{
		"sub":   subject,
		"exp":   time.Now().Add(expiration).Unix(),
		"iat":   time.Now().Unix(),
		"email": "test@example.com",
	}
	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}
