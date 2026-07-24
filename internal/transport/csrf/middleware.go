// Package csrf はDouble Submit CookieパターンによるCSRF保護を提供します。
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

const (
	// CookieName はCSRFトークンを格納するCookie名です。
	// このCookieはhttpOnly=falseのため、JavaScriptから読み取り可能です。
	CookieName = "csrf_token"

	// HeaderName はCSRFトークンを送信するリクエストヘッダー名です。
	HeaderName = "X-CSRF-Token"

	tokenBytes = 32
)

// GenerateToken は暗号学的に安全な64文字のhex文字列（CSRFトークン）を生成します。
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Protect はDouble Submit CookieパターンでCSRF攻撃を防ぐミドルウェアを返します。
//   - GET / HEAD / OPTIONS などの安全なメソッドはスキップします
//   - Bearer認証（Authorization: Bearer）の場合はスキップします（CSRFはCookieベース認証にのみ必要）
//   - それ以外のメソッドでは X-CSRF-Token ヘッダーと csrf_token Cookie の値が
//     一致しない場合に 403 を返します
func Protect() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			// Bearer認証の場合はCSRFチェックをスキップ
			// （CSRFはブラウザのCookie自動送信を悪用する攻撃のため、明示的なAuthorizationヘッダーを使う場合は不要）
			if jwt.AuthSourceFromContext(r.Context()) == jwt.AuthSourceBearer {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(CookieName)
			if err != nil || cookie.Value == "" {
				httpx.WriteJSON(w, http.StatusForbidden, api.ErrorResponse{Error: "missing csrf token"})
				return
			}

			if !requireHeaderMatch(w, r, cookie.Value) {
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ProtectIfCookiePresent は forced logout CSRF対策のミドルウェアです。認証必須にできない
// logoutエンドポイント向けに、csrf_token Cookieを持つリクエストのみヘッダー照合を要求します（issue #330）。
func ProtectIfCookiePresent() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(CookieName)
			if err != nil || cookie.Value == "" {
				// Cookieがない（既にログアウト済み・Bearer認証等）場合はチェックをスキップ
				next.ServeHTTP(w, r)
				return
			}

			if !requireHeaderMatch(w, r, cookie.Value) {
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requireHeaderMatch はX-CSRF-TokenヘッダーとcsrfCookieValueを定数時間比較します。
// 一致すればtrueを返し、不一致なら403を書き込んでfalseを返します。
func requireHeaderMatch(w http.ResponseWriter, r *http.Request, csrfCookieValue string) bool {
	headerVal := r.Header.Get(HeaderName)
	if headerVal == "" || subtle.ConstantTimeCompare([]byte(headerVal), []byte(csrfCookieValue)) != 1 {
		httpx.WriteJSON(w, http.StatusForbidden, api.ErrorResponse{Error: "csrf token mismatch"})
		return false
	}
	return true
}
