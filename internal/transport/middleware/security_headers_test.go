package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("共通のセキュリティヘッダーを常に設定する", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		SecurityHeaders(false)(next).ServeHTTP(rec, req)

		h := rec.Header()
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
		assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"))
		assert.Equal(t, "default-src 'none'", h.Get("Content-Security-Policy"))
	})

	t.Run("enableHSTS=false のとき HSTS を付与しない", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		SecurityHeaders(false)(next).ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("enableHSTS=true のとき HSTS を付与する", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		SecurityHeaders(true)(next).ServeHTTP(rec, req)

		assert.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	})
}
