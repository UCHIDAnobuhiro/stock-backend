package jwt

import (
	"context"
	"net/http"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// RevokeRequestToken はリクエストに含まれるJWT（Cookie優先、次にAuthorizationヘッダー）を
// 検証し、有効なトークンであればjtiをブラックリストに登録して有効期限前でも即時失効させます。
// トークンが存在しない・署名が無効・既に期限切れの場合は失効させる対象がないため何もせず nil を返します
// （ログアウトはCookie削除が主目的であり、それらのケースでも成功扱いにしてよいため）。
func RevokeRequestToken(ctx context.Context, r *http.Request, secret string, blacklist *Blacklist) error {
	tokenStr, _ := ExtractToken(r)
	if tokenStr == "" {
		return nil
	}

	token, err := parseToken(secret, tokenStr)
	if err != nil || !token.Valid {
		return nil
	}

	claims, ok := token.Claims.(gojwt.MapClaims)
	if !ok {
		return nil
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil
	}

	expUnix, ok := claims["exp"].(float64)
	if !ok {
		return nil
	}
	ttl := time.Until(time.Unix(int64(expUnix), 0))

	return blacklist.Revoke(ctx, jti, ttl)
}
