// Package httpx は net/http ハンドラー向けの共通ユーティリティ
// （JSON レスポンス書き出し・JSON ボディデコード・クライアント IP 取得）を提供します。
package httpx

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"net"
	"net/http"
)

// clientIPKey は context に解決済みクライアント IP を格納する際のキー型です。
// 他パッケージのキーと衝突しないよう非公開の型にします。
type clientIPKey struct{}

// WriteJSON は status コードと共に v を JSON としてレスポンスへ書き込みます。
// エンコードに失敗した場合は、部分的なレスポンスを送らず 500 を返します。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if v == nil {
		w.WriteHeader(status)
		return
	}
	body, err := json.Marshal(
		v,
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
	)
	if err != nil {
		slog.Error("failed to encode JSON response")
		status = http.StatusInternalServerError
		body = []byte(`{"error":"internal server error"}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// DecodeJSON はリクエストボディを JSON として dst にデコードします。
// スキーマに基づくバリデーション（required / format / minLength 等）は
// OpenAPI バリデーションミドルウェア（internal/transport/openapivalidate）が
// ハンドラ到達前に実施するため、ここでは型へのデコードのみを行います。
// JSON 構文エラー等のデコード失敗時はエラーを返します。
func DecodeJSON(r *http.Request, dst any) error {
	return json.UnmarshalRead(r.Body, dst)
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
