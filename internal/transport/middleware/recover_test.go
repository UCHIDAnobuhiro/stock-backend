package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
)

// TestRecover は Recover ミドルウェアが panic を 500 レスポンスへ変換すること、
// panic が発生しない場合は後続ハンドラーの結果をそのまま透過させることを検証します。
func TestRecover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		next          http.HandlerFunc
		wantPanicVal  any // non-nil の場合、ServeHTTP 呼び出し自体が panic することを期待する
		wantStatus    int
		wantErrBody   string
		checkJSONBody bool
	}{
		{
			name: "success: no panic passes status and body through unchanged",
			next: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte("hello"))
			},
			wantStatus:    http.StatusTeapot,
			checkJSONBody: false,
		},
		{
			name: "success: panic with string value returns 500 JSON error",
			next: func(_ http.ResponseWriter, _ *http.Request) {
				panic("something went wrong")
			},
			wantStatus:    http.StatusInternalServerError,
			wantErrBody:   "internal server error",
			checkJSONBody: true,
		},
		{
			name: "success: panic with error value returns 500",
			next: func(_ http.ResponseWriter, _ *http.Request) {
				panic(errors.New("boom"))
			},
			wantStatus:    http.StatusInternalServerError,
			wantErrBody:   "internal server error",
			checkJSONBody: true,
		},
		{
			name: "success: panic(nil) returns 500",
			next: func(_ http.ResponseWriter, _ *http.Request) {
				// Go 1.21+ では panic(nil) は recover() で *runtime.PanicNilError になる。
				panic(nil) //nolint:govet // 意図的に panic(nil) の挙動を検証する
			},
			wantStatus:    http.StatusInternalServerError,
			wantErrBody:   "internal server error",
			checkJSONBody: true,
		},
		{
			name: "success: panic(http.ErrAbortHandler) re-panics with the same value",
			next: func(_ http.ResponseWriter, _ *http.Request) {
				panic(http.ErrAbortHandler)
			},
			wantPanicVal: http.ErrAbortHandler,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var called bool
			wrapped := Recover()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				tt.next(w, r)
			}))

			req := httptest.NewRequest(http.MethodGet, "/some/path", nil)
			rr := httptest.NewRecorder()

			if tt.wantPanicVal != nil {
				require.PanicsWithValue(t, tt.wantPanicVal, func() {
					wrapped.ServeHTTP(rr, req)
				})
				assert.True(t, called, "next handler should have been invoked before panicking")
				return
			}

			require.NotPanics(t, func() {
				wrapped.ServeHTTP(rr, req)
			})
			assert.True(t, called, "next handler should have been invoked")
			assert.Equal(t, tt.wantStatus, rr.Code)

			if tt.checkJSONBody {
				assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))
				var errResp api.ErrorResponse
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &errResp))
				assert.Equal(t, tt.wantErrBody, errResp.Error)
				return
			}

			assert.Equal(t, "hello", rr.Body.String())
		})
	}
}
