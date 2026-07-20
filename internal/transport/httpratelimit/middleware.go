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

// RateLimitConfig はレートリミットの設定を保持します（キー方式に依存しない共通設定）。
type RateLimitConfig struct {
	Prefix string        // Redisキーのプレフィックス（例: "rl:login:ip"）
	Limit  int           // ウィンドウ内の最大リクエスト数
	Window time.Duration // スライディングウィンドウの時間幅
	// Policy はRedis未接続・障害時の挙動です（FailOpen: 許可 / FailClosed: 503で拒否）。
	// ゼロ値はFailClosed（secure by default）です。非クリティカルな用途でfail-openに
	// したい場合はFailOpenを明示的に指定してください。
	Policy Policy
}

// writeRateLimitResult は Allow() の結果に応じて 503/429 レスポンスを書き込む共通処理です。
// ログ属性はキー方式ごとに異なるため、呼び出し側から logType と追加の slog 属性を受け取ります。
// 許可された場合は何も書き込みません（呼び出し側が next.ServeHTTP を続行します）。
func writeRateLimitResult(w http.ResponseWriter, result Result, logType string, logArgs ...any) {
	if result.ServiceUnavailable {
		slog.Error("rate limiter unavailable, rejecting request",
			append([]any{"type", logType}, logArgs...)...,
		)
		httpx.WriteJSON(w, http.StatusServiceUnavailable, api.ErrorResponse{
			Error: "service temporarily unavailable",
		})
		return
	}
	slog.Warn("rate limit exceeded",
		append([]any{"type", logType}, logArgs...)...,
	)
	w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
	httpx.WriteJSON(w, http.StatusTooManyRequests, api.ErrorResponse{
		Error: "too many requests",
	})
}

// ByIP はIPアドレスベースのレートリミットミドルウェアを返します。
func ByIP(limiter *Limiter, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := httpx.ClientIP(r)
			key := fmt.Sprintf("%s:%s", cfg.Prefix, ip)
			result := limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window, cfg.Policy)

			if !result.Allowed {
				writeRateLimitResult(w, result, "ip", "ip", ip, "prefix", cfg.Prefix)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ByUserID は認証済みユーザーIDベースのレートリミットミドルウェアを返します。
// jwt.AuthRequired より後段に配置する必要があります。
func ByUserID(limiter *Limiter, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := jwt.UserIDFromContext(r.Context())
			if !ok {
				slog.Error("rate limiter: user id not found in context (AuthRequired must run before ByUserID)",
					"type", "user",
					"prefix", cfg.Prefix,
				)
				httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{
					Error: "internal server error",
				})
				return
			}

			key := fmt.Sprintf("%s:%d", cfg.Prefix, userID)
			result := limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window, cfg.Policy)

			if !result.Allowed {
				writeRateLimitResult(w, result, "user", "user_id", userID, "prefix", cfg.Prefix)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
