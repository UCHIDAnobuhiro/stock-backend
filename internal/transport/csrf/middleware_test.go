package csrf

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

const testSecret = "test-secret"

// newRecordingHandler returns a next handler that records whether it was called.
func newRecordingHandler() (http.Handler, *bool) {
	called := new(bool)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
	return h, called
}

// signToken issues a valid HS256 JWT signed with the given secret.
func signToken(t *testing.T, secret, sub string) string {
	t.Helper()
	claims := gojwt.MapClaims{
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

func TestProtect_SafeMethodsSkipped(t *testing.T) {
	t.Parallel()

	methods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			next, called := newRecordingHandler()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/", nil)

			Protect()(next).ServeHTTP(w, req)

			assert.True(t, *called, "next must be called for safe methods")
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestProtect_RejectsMissingOrMismatchedToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cookieValue string
		setCookie   bool
		headerValue string
	}{
		{name: "missing cookie", setCookie: false, headerValue: "token"},
		{name: "empty cookie", setCookie: true, cookieValue: "", headerValue: "token"},
		{name: "missing header", setCookie: true, cookieValue: "token", headerValue: ""},
		{name: "mismatch", setCookie: true, cookieValue: "token", headerValue: "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			next, called := newRecordingHandler()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.setCookie {
				req.AddCookie(&http.Cookie{Name: CookieName, Value: tt.cookieValue})
			}
			if tt.headerValue != "" {
				req.Header.Set(HeaderName, tt.headerValue)
			}

			Protect()(next).ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.False(t, *called, "next must not be called when CSRF check fails")
		})
	}
}

func TestProtect_AllowsMatchingToken(t *testing.T) {
	t.Parallel()

	const token = "matching-csrf-token"
	next, called := newRecordingHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	req.Header.Set(HeaderName, token)

	Protect()(next).ServeHTTP(w, req)

	assert.True(t, *called, "next must be called when tokens match")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtect_BearerAuthSkipsCheck(t *testing.T) {
	t.Parallel()

	next, called := newRecordingHandler()
	chain := jwt.AuthRequired(testSecret, nil)(Protect()(next))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, testSecret, "1"))

	chain.ServeHTTP(w, req)

	assert.True(t, *called, "next must be called for bearer auth (CSRF skipped)")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtectIfCookiePresent_SafeMethodsSkipped(t *testing.T) {
	t.Parallel()

	methods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			next, called := newRecordingHandler()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/", nil)

			ProtectIfCookiePresent()(next).ServeHTTP(w, req)

			assert.True(t, *called, "next must be called for safe methods")
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestProtectIfCookiePresent_NoCookieSkipsCheck(t *testing.T) {
	t.Parallel()

	next, called := newRecordingHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)

	ProtectIfCookiePresent()(next).ServeHTTP(w, req)

	assert.True(t, *called, "next must be called when csrf_token cookie is absent")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtectIfCookiePresent_EmptyCookieSkipsCheck(t *testing.T) {
	t.Parallel()

	next, called := newRecordingHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: ""})

	ProtectIfCookiePresent()(next).ServeHTTP(w, req)

	assert.True(t, *called, "next must be called when csrf_token cookie is empty")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtectIfCookiePresent_RejectsMissingOrMismatchedHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		headerValue string
	}{
		{name: "missing header", headerValue: ""},
		{name: "mismatch", headerValue: "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			next, called := newRecordingHandler()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/", nil)
			req.AddCookie(&http.Cookie{Name: CookieName, Value: "token"})
			if tt.headerValue != "" {
				req.Header.Set(HeaderName, tt.headerValue)
			}

			ProtectIfCookiePresent()(next).ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.False(t, *called, "next must not be called when CSRF check fails")
		})
	}
}

func TestProtectIfCookiePresent_AllowsMatchingToken(t *testing.T) {
	t.Parallel()

	const token = "matching-csrf-token"
	next, called := newRecordingHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	req.Header.Set(HeaderName, token)

	ProtectIfCookiePresent()(next).ServeHTTP(w, req)

	assert.True(t, *called, "next must be called when tokens match")
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestProtect_CookieAuthWithForgedBearerStillRequiresCSRF is the regression for
// issue #201: even when a forged Authorization: Bearer header is present, cookie
// auth takes priority (auth_source == "cookie"), so the CSRF check must still run.
func TestProtect_CookieAuthWithForgedBearerStillRequiresCSRF(t *testing.T) {
	t.Parallel()

	authToken := signToken(t, testSecret, "1")

	t.Run("without csrf token is rejected", func(t *testing.T) {
		t.Parallel()

		next, called := newRecordingHandler()
		chain := jwt.AuthRequired(testSecret, nil)(Protect()(next))

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: authToken})
		req.Header.Set("Authorization", "Bearer forged-token")

		chain.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.False(t, *called, "next must not be called: CSRF is required for cookie auth")
	})

	t.Run("with matching csrf token passes", func(t *testing.T) {
		t.Parallel()

		const csrfToken = "valid-csrf-token"
		next, called := newRecordingHandler()
		chain := jwt.AuthRequired(testSecret, nil)(Protect()(next))

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: authToken})
		req.AddCookie(&http.Cookie{Name: CookieName, Value: csrfToken})
		req.Header.Set("Authorization", "Bearer forged-token")
		req.Header.Set(HeaderName, csrfToken)

		chain.ServeHTTP(w, req)

		assert.True(t, *called, "next must be called when CSRF token matches")
		assert.Equal(t, http.StatusOK, w.Code)
	})
}
