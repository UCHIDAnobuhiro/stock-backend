package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// AccessLog は各 HTTP リクエストを slog の構造化ログとして出力するミドルウェアを返します。
// Cloud Logging が解釈できる httpRequest フィールドを出力します。
//
// トレース相関フィールドは、TraceID ミドルウェアが ctx に設定したトレース情報を
// logging.ContextHandler がログ出力時に自動で付与するため、ここでは扱いません。
func AccessLog() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			path := r.URL.Path
			if raw := r.URL.RawQuery; raw != "" {
				path = path + "?" + raw
			}

			// ステータスコードと書き込みバイト数を捕捉するためレスポンスライターをラップする。
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				// ハンドラーが WriteHeader を呼ばなかった場合、net/http は 200 を返す。
				status = http.StatusOK
			}
			latency := time.Since(start)
			responseSize := ww.BytesWritten()

			// Cloud Logging の httpRequest 構造化フィールド。
			// latency は protobuf Duration の JSON 表現（秒 + "s"）、サイズ系は文字列で表す。
			attrs := []slog.Attr{
				slog.Any("httpRequest", map[string]any{
					"requestMethod": r.Method,
					"requestUrl":    path,
					"status":        status,
					"responseSize":  fmt.Sprintf("%d", responseSize),
					"userAgent":     r.UserAgent(),
					"remoteIp":      httpx.ClientIP(r),
					"protocol":      r.Proto,
					"latency":       fmt.Sprintf("%.9fs", latency.Seconds()),
				}),
			}

			// 5xx はハンドラー側のエラーログと Error Reporting で重複するため除外する。
			attrs = append(attrs, logging.SkipErrorReport())

			slog.LogAttrs(r.Context(), severityForStatus(status), "request", attrs...)
		})
	}
}

// severityForStatus は HTTP ステータスコードを slog のレベルに対応付けます。
// 5xx は Error、4xx は Warn、それ以外は Info とします。
func severityForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
