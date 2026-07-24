// Package jwt はJWTトークンの生成と認証ミドルウェアを提供します。
package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// DefaultTokenTTL はアクセストークン（JWT）の有効期限です。
// トークンの exp クレームと auth_token / csrf_token Cookie の Max-Age は
// 必ずこの値から導出し、有効期限の定義を一箇所に集約します。
const DefaultTokenTTL = time.Hour

// Generator はJWTトークンの生成を実装します。
// 利用者（例: auth/usecase）が定義するJWTGeneratorインターフェースを実装します。
type Generator struct {
	secret     []byte
	expiration time.Duration
}

// NewGenerator は指定されたシークレットと有効期限でJWTジェネレータの新しいインスタンスを生成します。
func NewGenerator(secret string, expiration time.Duration) *Generator {
	return &Generator{
		secret:     []byte(secret),
		expiration: expiration,
	}
}

// GenerateToken は標準クレームを含む署名済みJWTトークンを生成します。
// jti（JWT ID）を付与することで、ログアウト時にRedisブラックリストへ登録して
// 有効期限前でも個々のトークンを即時失効させられるようにします。
func (g *Generator) GenerateToken(userID int64, email string) (string, error) {
	jti, err := generateJTI()
	if err != nil {
		return "", fmt.Errorf("failed to generate jti: %w", err)
	}

	claims := gojwt.MapClaims{
		"sub":   strconv.FormatInt(userID, 10),
		"exp":   time.Now().Add(g.expiration).Unix(),
		"iat":   time.Now().Unix(),
		"email": email,
		"jti":   jti,
	}

	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(g.secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return signed, nil
}

// generateJTI は暗号学的に安全な32文字のhex文字列（jti）を生成します。
func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
