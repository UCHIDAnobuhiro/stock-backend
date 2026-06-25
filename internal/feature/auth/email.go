package auth

import "strings"

// NormalizeEmail はメールアドレスを保存・検索の前段で正規化します。
// 前後の空白を除去し、すべて小文字化することで、
// `User@Example.com ` と `user@example.com` を同一のメールとして扱います。
// これにより重複アカウントの作成や OAuth 自動リンクの不一致を防ぎます。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
