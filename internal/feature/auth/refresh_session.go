package auth

import "time"

// DefaultRefreshTokenTTL はリフレッシュトークンの有効期限です。
const DefaultRefreshTokenTTL = 30 * 24 * time.Hour

// TokenPair はブラウザセッションへ発行するアクセストークンと
// リフレッシュトークンをまとめた値です。
type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// RefreshSession はサーバー管理のリフレッシュセッションです。
// TokenHash にはトークン本体ではなく SHA-256 ハッシュだけを保持します。
type RefreshSession struct {
	ID         string
	FamilyID   string
	UserID     int64
	TokenHash  []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	RevokedAt  *time.Time
	ReplacedBy *string
	CreatedAt  time.Time
}
