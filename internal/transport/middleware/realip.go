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
// trustedHops は「信頼できるリバースプロキシの段数」です。Cloud Run に直接公開する
// 構成では Google Front End (GFE) が唯一の信頼できるプロキシであり、GFE は受け取った
// リクエストの X-Forwarded-For 末尾に実クライアントIPを追記するため trustedHops=1 を
// 設定します。将来、GFE の手前に外部ロードバランサーを追加すればプロキシが1段増えるため
// trustedHops=2 とします。
//
// X-Forwarded-For の値は「クライアント, プロキシ1, プロキシ2, ...」の順（左が古い）で
// 各ホップが手前の値の右側に自身が観測した送信元を追記していく想定のため、信頼できる
// プロキシが n 段なら実クライアントIPは右から n 番目のエントリになります。
// クライアントが偽装した X-Forwarded-For を送っても、信頼するプロキシがその右側に
// 自身の観測値を追記する限り、偽装エントリは右から n 番目より左に押し出されるため
// 改ざんできません（信頼できないプロキシの外側にクライアントを直接到達させない前提）。
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
		for _, part := range strings.Split(line, ",") {
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
