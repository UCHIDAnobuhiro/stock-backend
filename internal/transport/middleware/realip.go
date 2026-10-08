package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// RealIP は X-Forwarded-For ヘッダーから実クライアントIPを解決し、
// httpx.WithClientIP で context に格納するミドルウェアを返します。
//
// trustedHops は X-Forwarded-For の右端から実クライアントIPまでのエントリ数です。
// Cloud Run 直接公開では末尾のクライアントIPを使うため1を設定します。
// 外部Application Load Balancerが末尾に client-ip,load-balancer-ip を追加する構成では2です。
// 値はプロキシの台数ではなく、アプリケーションが受信するヘッダーに合わせて設定します。
// 信頼する経路を迂回して直接到達できる場合は、左側の偽装エントリを選ばないように
// 信頼するエントリ数を増やしてはいけません。
//
// trustedHops <= 0 の場合は X-Forwarded-For を一切信頼せず、何もしないパススルーを返します
// （httpx.ClientIP は RemoteAddr にフォールバックします）。
func RealIP(trustedHops int) func(http.Handler) http.Handler {
	if trustedHops <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip, ok := resolveClientIP(r, trustedHops); ok {
				r = r.WithContext(httpx.WithClientIP(r.Context(), ip))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveClientIP はリクエストの全 X-Forwarded-For ヘッダー値を結合し、
// 右から trustedHops 番目のエントリを実クライアントIPとして返します。
// エントリ数が不足する場合や該当エントリが不正なIPの場合は ok=false を返します。
func resolveClientIP(r *http.Request, trustedHops int) (ip string, ok bool) {
	xff := r.Header.Values("X-Forwarded-For")
	if len(xff) == 0 {
		return "", false
	}

	var entries []string
	for _, line := range xff {
		for part := range strings.SplitSeq(line, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				entries = append(entries, trimmed)
			}
		}
	}

	idx := len(entries) - trustedHops
	if idx < 0 || idx >= len(entries) {
		return "", false
	}

	candidate := entries[idx]
	if net.ParseIP(candidate) == nil {
		return "", false
	}
	return candidate, true
}
