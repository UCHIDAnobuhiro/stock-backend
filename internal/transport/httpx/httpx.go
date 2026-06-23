// Package httpx は net/http ハンドラー向けの共通ユーティリティ
// （JSON レスポンス書き出し・JSON ボディデコード・クライアント IP 取得）を提供します。
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
)

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

// ClientIP はリクエスト元のIPアドレスを返します。
// X-Forwarded-For 等のプロキシヘッダーは信頼せず、TCP接続元（RemoteAddr）の
// ホスト部のみを返します。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// ポートが付与されていない場合は RemoteAddr をそのまま返す。
		return r.RemoteAddr
	}
	return host
}
