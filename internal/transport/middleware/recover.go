package middleware

import (
	"log/slog"
	"net/http"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// Recover はハンドラー内で発生した panic を回復し、500 を返すミドルウェアを返します。
// AccessLog の内側に配置することで panic を 500 に変換した結果も
// アクセスログに記録されます。
func Recover() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler は標準サーバが特別扱いする規約のため、握りつぶさず伝播させる。
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					slog.Error("panic recovered", "error", rec, "path", r.URL.Path, "method", r.Method)
					httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
