// Package httpx は net/http ハンドラー向けの共通ユーティリティ
// （JSON レスポンス書き出し・JSON ボディデコード・クライアント IP 取得）を提供します。
package httpx

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
)

// clientIPKey は context に解決済みクライアント IP を格納する際のキー型です。
// 他パッケージのキーと衝突しないよう非公開の型にします。
type clientIPKey struct{}

// WriteJSON は status コードと共に v を JSON としてレスポンスへ書き込みます。
// エンコードに失敗した場合はステータス設定後のため
// それ以上の回復はできず、呼び出し側の責務として v は常にエンコード可能であることを前提とします。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON はリクエストボディを JSON として dst にデコードします。
// スキーマに基づくバリデーション（required / format / minLength 等）は
// OpenAPI バリデーションミドルウェア（internal/transport/openapivalidate）が
// ハンドラ到達前に実施するため、ここでは型へのデコードのみを行います。
// JSON 構文エラー等のデコード失敗時はエラーを返します。
func DecodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// WithClientIP は解決済みのクライアント IP を context に格納します。
// X-Forwarded-For の解決は middleware.RealIP が行い、その結果をここで
// context に載せることで、以降のハンドラー・ミドルウェアが ClientIP 経由で
// 参照できるようにします（httpx 自身はプロキシヘッダーを解釈しません）。
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIP はリクエスト元のIPアドレスを返します。
// context に middleware.RealIP が解決したIPが格納されていればそれを返し、
// なければ TCP接続元（RemoteAddr）のホスト部にフォールバックします。
// X-Forwarded-For 等のプロキシヘッダー自体はここでは解釈しません
// （信頼するプロキシ段数に基づく解釈は middleware.RealIP の責務です）。
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// ポートが付与されていない場合は RemoteAddr をそのまま返す。
		return r.RemoteAddr
	}
	return host
}
