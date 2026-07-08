package auth

import "time"

// OAuthAccount はOAuth2プロバイダーとユーザーを紐付けるエンティティです。
// (provider, provider_uid) の複合ユニーク制約でプロバイダー側IDの重複を防ぎます。
type OAuthAccount struct {
	// UserID は紐付けられたユーザーのIDです。
	UserID int64

	// Provider はOAuth2プロバイダー名です（"google" | "github"）。
	Provider string

	// ProviderUID はプロバイダー側のユーザー一意IDです。
	// Google: "sub" クレーム / GitHub: ユーザーの数値ID（文字列）
	ProviderUID string

	CreatedAt time.Time
	UpdatedAt time.Time
}
