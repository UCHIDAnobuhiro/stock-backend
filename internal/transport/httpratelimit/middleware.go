package httpratelimit

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// IPRateLimitConfig はIPベースのレートリミットの設定を保持します。
type IPRateLimitConfig struct {
	Prefix string        // Redisキーのプレフィックス（例: "rl:login:ip"）
	Limit  int           // ウィンドウ内の最大リクエスト数
	Window time.Duration // スライディングウィンドウの時間幅
}

// ByIP はIPアドレスベースのレートリミットミドルウェアを返します。
func ByIP(limiter *Limiter, cfg IPRateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := httpx.ClientIP(r)
			key := fmt.Sprintf("%s:%s", cfg.Prefix, ip)
			result := limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window)

			if !result.Allowed {
				slog.Warn("rate limit exceeded",
					"type", "ip",
					"ip", ip,
					"prefix", cfg.Prefix,
				)
				w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
				httpx.WriteJSON(w, http.StatusTooManyRequests, api.ErrorResponse{
					Error: "too many requests",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UserRateLimitConfig は認証済みユーザーIDベースのレートリミットの設定を保持します。
type UserRateLimitConfig struct {
	Prefix string        // Redisキーのプレフィックス（例: "rl:logo:user"）
	Limit  int           // ウィンドウ内の最大リクエスト数
	Window time.Duration // スライディングウィンドウの時間幅
}

// ByUser は jwt.AuthRequired が context に格納した認証済みユーザーIDをキーとする
// レートリミットミドルウェアを返します。jwt.AuthRequired の後段に配置してください。
// context にユーザーIDが存在しない場合は警告ログを出力してリクエストを通過させます
// （グレースフルデグレード）。
func ByUser(limiter *Limiter, cfg UserRateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := jwt.UserIDFromContext(r.Context())
			if !ok {
				slog.Warn("user id not found in context, skipping rate limit",
					"type", "user",
					"prefix", cfg.Prefix,
				)
				next.ServeHTTP(w, r)
				return
			}

			key := fmt.Sprintf("%s:%d", cfg.Prefix, userID)
			result := limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window)

			if !result.Allowed {
				slog.Warn("rate limit exceeded",
					"type", "user",
					"user_id", userID,
					"prefix", cfg.Prefix,
				)
				w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
				httpx.WriteJSON(w, http.StatusTooManyRequests, api.ErrorResponse{
					Error: "too many requests",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
