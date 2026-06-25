// Package logging は構造化ログ出力時の機密情報マスク用ヘルパーを提供します。
package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
)

// hashedEmailLen はログ出力するメールハッシュの文字数（hex）です。
// 48 bit 相当で、同一性比較と PII 非可逆性のバランスを取った値です。
const hashedEmailLen = 12

// HashedEmail はメールアドレスを slog 出力時に SHA-256 ハッシュ（先頭12文字）に
// 自動変換するラッパー型です。同一メールは同一ハッシュになるため、
// PII を残さずに連続失敗・レート制限などの相関解析が可能です。
type HashedEmail string

// LogValue は slog による構造化ログ出力時にメールアドレスをハッシュ化します。
// 空文字列はそのまま空文字列として出力します（誤判定回避のため）。
func (e HashedEmail) LogValue() slog.Value {
	if e == "" {
		return slog.StringValue("")
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(string(e)))))
	return slog.StringValue(hex.EncodeToString(sum[:])[:hashedEmailLen])
}
