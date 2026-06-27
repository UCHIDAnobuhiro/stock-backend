// Package middleware はプラットフォーム共通のHTTPミドルウェアを提供します。
package middleware

import "net/http"

// hstsValue は HSTS（HTTP Strict Transport Security）の値です。
// max-age は2年（63072000秒）。サブドメインも対象に含めます。
const hstsValue = "max-age=63072000; includeSubDomains"

// SecurityHeaders はセキュリティ関連のHTTPレスポンスヘッダーを設定するミドルウェアを返します。
// このAPIサーバーはJSONのみを返すため、CSPは最も制限的な設定を使用します。
//
// enableHSTS が true の場合、Strict-Transport-Security ヘッダーを付与します。
// TLS を終端する本番構成（COOKIE_SECURE=true / APP_ENV=production）でのみ有効化し、
// 平文HTTPで開発する際に誤って HSTS をブラウザにキャッシュさせないようにします。
func SecurityHeaders(enableHSTS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Content-Security-Policy", "default-src 'none'")
			if enableHSTS {
				h.Set("Strict-Transport-Security", hstsValue)
			}
			next.ServeHTTP(w, r)
		})
	}
}
