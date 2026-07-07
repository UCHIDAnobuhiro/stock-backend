package jwt

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// ExtractToken はリクエストからJWT文字列を取り出します。
// Cookie（auth_token）を優先し、存在しない場合はAuthorizationヘッダー（Bearer）に
// フォールバックします。トークンが見つからない場合は空文字列を返します。
func ExtractToken(r *http.Request) (tokenStr, authSource string) {
	// 1. auth_token Cookie を優先（Next.jsブラウザクライアント用）
	if cookie, err := r.Cookie("auth_token"); err == nil && cookie.Value != "" {
		return cookie.Value, AuthSourceCookie
	}
	// 2. Authorization: Bearer ヘッダーにフォールバック（APIクライアント・curl等）
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer "), AuthSourceBearer
	}
	return "", ""
}

// parseToken はJWT署名を検証し、HMACアルゴリズムで署名されたトークンのみを受理します。
func parseToken(secret, tokenStr string) (*gojwt.Token, error) {
	return gojwt.Parse(tokenStr, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, gojwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
}

// AuthRequired はJWTトークンを検証し、認証済みユーザーのみにアクセスを制限するミドルウェアを返します。
// 認証はCookie（auth_token）を優先し、存在しない場合はAuthorizationヘッダーにフォールバックします。
// 署名シークレットは起動時に注入されます（環境変数の読み込みは internal/app/config に集約）。
// secret が空の場合は全リクエストを 500（サーバー設定ミス）として扱います。
// blacklist はログアウト済みトークン（jti）の即時失効チェックに使用します。nil可（未失効扱い）。
func AuthRequired(secret string, blacklist *Blacklist) func(http.Handler) http.Handler {
	if secret == "" {
		// サーバー設定ミス（JWT_SECRETが未設定）。通常は LoadAPI が起動時に必須を
		// 強制するため到達しないが、多層防御として全リクエストを 500 にする。
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "server misconfigured"})
			})
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, authSource := ExtractToken(r)
			if tokenStr == "" {
				httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "missing authentication token"})
				return
			}

			// 3. JWT署名をパースして検証
			token, err := parseToken(secret, tokenStr)
			if err != nil || !token.Valid {
				// 検証エラーまたは無効なトークン
				httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid token"})
				return
			}

			// 4. クレーム（ペイロード）を抽出
			claims, ok := token.Claims.(gojwt.MapClaims)
			if !ok {
				httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid token claims"})
				return
			}
			userID, err := parseSubject(claims["sub"])
			if err != nil {
				httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid token: invalid subject"})
				return
			}

			// 5. ログアウトにより失効済みのトークンでないか確認
			jti, _ := claims["jti"].(string)
			if blacklist.IsRevoked(r.Context(), jti) {
				httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "token has been revoked"})
				return
			}

			// 6. ユーザーIDと認証方式を context に格納し、次のハンドラーへ制御を渡す
			ctx := WithUserID(r.Context(), userID)
			ctx = withAuthSource(ctx, authSource)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// parseSubject はJWT subjectをユーザーIDへ変換します。
// 新規トークンは文字列を使用しますが、移行中の既存トークン向けに安全な範囲の数値も受理します。
func parseSubject(claim any) (int64, error) {
	switch sub := claim.(type) {
	case string:
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil || userID <= 0 {
			return 0, errors.New("subject must be a positive integer")
		}
		return userID, nil
	case float64:
		const maxSafeInteger = float64(1<<53 - 1)
		if sub <= 0 || sub > maxSafeInteger || math.Trunc(sub) != sub {
			return 0, errors.New("numeric subject must be a safe positive integer")
		}
		return int64(sub), nil
	default:
		return 0, errors.New("subject must be a string or number")
	}
}
