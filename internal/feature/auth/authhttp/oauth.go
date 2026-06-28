package authhttp

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/csrf"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// oauthStateCookie はOAuth state をブラウザに紐付けるための短命 Cookie 名です。
// この Cookie の値とコールバック時のクエリ state を照合することで、ログイン CSRF /
// セッションフィクセーションを防ぎます。
const oauthStateCookie = "oauth_state"

// oauthStateCookieMaxAge は state Cookie の有効期限（秒）です。
// 認可フロー完了までの猶予として usecase 側の state TTL（10分）と揃えます。
const oauthStateCookieMaxAge = 600

// OAuthUsecase はOAuth2認証フローのユースケースインターフェースです。
// Goの慣例に従い、インターフェースはプロバイダー（usecase）ではなくコンシューマー（handler）が定義します。
type OAuthUsecase interface {
	BeginAuth(ctx context.Context, provider string) (authURL, state string, err error)
	HandleCallback(ctx context.Context, provider, code, state string) (token string, err error)
}

// OAuthHandler はOAuth2フローのHTTPリクエストを処理します。
type OAuthHandler struct {
	oauth        OAuthUsecase
	secureCookie bool
	frontendURL  string // OAUTH_FRONTEND_REDIRECT_URL: 認証完了後のリダイレクト先
}

// NewOAuthHandler はOAuthHandlerの新しいインスタンスを生成します。
func NewOAuthHandler(oauth OAuthUsecase, secureCookie bool, frontendURL string) *OAuthHandler {
	return &OAuthHandler{
		oauth:        oauth,
		secureCookie: secureCookie,
		frontendURL:  frontendURL,
	}
}

// BeginAuth はOAuth2認可フローを開始します。
// プロバイダーの認可画面へリダイレクトします。
func (h *OAuthHandler) BeginAuth(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	authURL, state, err := h.oauth.BeginAuth(r.Context(), provider)
	if err != nil {
		slog.Warn("oauth begin: failed", "provider", provider, "error", err)
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "unsupported provider"})
		return
	}

	// state をブラウザ側にも紐付ける（HttpOnly / SameSite=Lax / Secure の短命 Cookie）。
	// コールバック時にクエリの state とこの Cookie 値の一致を必須とすることで、
	// 攻撃者が取得した code+state を被害者に踏ませるログイン CSRF を防ぐ。
	setAuthCookie(w, oauthStateCookie, state, oauthStateCookieMaxAge, h.secureCookie, true)

	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback はOAuth2コールバックを処理します。
// stateの検証・コード交換・ユーザー作成/リンクを行い、JWTとCSRFトークンをCookieにセットして
// フロントエンドURLへリダイレクトします。
func (h *OAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "missing code or state"})
		return
	}

	// ブラウザ側 state Cookie との照合（ログイン CSRF / セッションフィクセーション対策）。
	// Cookie が欠落、またはクエリの state と一致しない場合は処理を中断する。
	// 一致しても定数時間比較で照合し、タイミング攻撃の余地を残さない。
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(state)) != 1 {
		slog.Warn("oauth callback: state cookie mismatch", "provider", provider)
		// 照合に失敗した場合でも state Cookie は不要になるため削除する。
		setAuthCookie(w, oauthStateCookie, "", -1, h.secureCookie, true)
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid or expired state"})
		return
	}

	// 照合に成功したので state Cookie は使い捨て（リプレイ防止のため削除）。
	setAuthCookie(w, oauthStateCookie, "", -1, h.secureCookie, true)

	token, err := h.oauth.HandleCallback(r.Context(), provider, code, state)
	if err != nil {
		if errors.Is(err, auth.ErrStateNotFound) {
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid or expired state"})
		} else if errors.Is(err, auth.ErrOAuthEmailUnavailable) {
			httpx.WriteJSON(w, http.StatusBadGateway, api.ErrorResponse{Error: "cannot obtain verified email from provider"})
		} else if errors.Is(err, auth.ErrUnknownProvider) {
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "unsupported provider"})
		} else {
			slog.Error("oauth callback failed", "provider", provider, "error", err)
			httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "oauth failed"})
		}
		return
	}

	// CSRFトークンを先に生成（失敗した場合はCookieをセットしない → 部分ログイン状態を防止）
	csrfToken, err := csrf.GenerateToken()
	if err != nil {
		slog.Error("failed to generate csrf token", "error", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal error"})
		return
	}

	slog.Info("oauth login successful", "provider", provider)

	// handler.go の Login と同一パターンで Cookie をセット
	setAuthCookie(w, "auth_token", token, 3600, h.secureCookie, true)
	setAuthCookie(w, "csrf_token", csrfToken, 3600, h.secureCookie, false)

	http.Redirect(w, r, h.frontendURL, http.StatusFound)
}
