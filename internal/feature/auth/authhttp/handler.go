package authhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/csrf"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// Usecase は認証操作のユースケースを定義します。
// Goの慣例に従い、インターフェースはプロバイダー（usecase）ではなくコンシューマー（handler）が定義します。
type Usecase interface {
	// Signup は指定されたメールアドレスとパスワードで新規ユーザーを登録し、作成されたユーザーIDを返します。
	Signup(ctx context.Context, email, password string) (int64, error)
	// Login はユーザーを認証し、成功時にトークンペアを返します。
	Login(ctx context.Context, email, password string) (auth.TokenPair, error)
	// Refresh はリフレッシュトークンを新しいトークンペアへ交換します。
	Refresh(ctx context.Context, refreshToken string) (auth.TokenPair, error)
	// Logout はリフレッシュトークンが属するセッション系列を失効させます。
	Logout(ctx context.Context, refreshToken string) error
}

// ログインのメールベースレートリミット設定
const (
	loginEmailLimit  = 5                // 15分間のメールアドレスあたりの最大ログイン試行回数
	loginEmailWindow = 15 * time.Minute // メールベースレートリミットのウィンドウ
)

const (
	authTokenCookieName    = "auth_token"
	refreshTokenCookieName = "refresh_token"
	authCookieMaxAge       = int(jwt.DefaultTokenTTL / time.Second)
	refreshCookieMaxAge    = int(auth.DefaultRefreshTokenTTL / time.Second)
)

// Handler は認証操作のHTTPリクエストを処理します。
// Usecaseインターフェースに依存し、JSONリクエスト/レスポンスを処理します。
type Handler struct {
	uc           Usecase
	limiter      *httpratelimit.Limiter
	secureCookie bool
	jwtSecret    string
	blacklist    *jwt.Blacklist
	postHooks    []auth.UserCreatedHook
}

// NewHandler はHandlerの新しいインスタンスを生成します。
// 依存性注入用のコンストラクタで、外部からUsecaseとレートリミッターを注入します。
// secureCookie が true の場合、Secure属性付きのCookieを設定します（本番環境用）。
// jwtSecret と blacklist はログアウト時のトークン即時失効（Logout）に使用します。
// postHooks にはサインアップ後に実行するフックを任意で渡せます。
func NewHandler(uc Usecase, limiter *httpratelimit.Limiter, secureCookie bool, jwtSecret string, blacklist *jwt.Blacklist, postHooks ...auth.UserCreatedHook) *Handler {
	return &Handler{uc: uc, limiter: limiter, secureCookie: secureCookie, jwtSecret: jwtSecret, blacklist: blacklist, postHooks: postHooks}
}

// Signup はユーザー登録APIエンドポイントを処理します。
// - リクエストJSONをSignupReqにバインド
// - バリデーションエラー時は400を返却
// - ユーザー作成失敗時（メール重複等）は409を返却
// - 成功時は201を返却
func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	var req api.SignupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		slog.Warn("signup validation failed", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid request"})
		return
	}
	userID, err := h.uc.Signup(r.Context(), req.Email, req.Password)
	if err != nil {
		// ユーザー列挙攻撃を防止するため、実際のエラーを公開しない
		slog.Warn("signup failed", "error", err, "email_hash", logging.HashedEmail(req.Email), "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusConflict, api.ErrorResponse{Error: "signup failed"})
		return
	}
	// 後処理フック呼び出し（例: ウォッチリスト初期化）
	// フック失敗はユーザー作成自体には影響しないため非致命的とし、ログのみ記録する。
	// ユーザー作成コミット後の後処理はクライアント切断（リクエストcontextのキャンセル）に
	// 影響されてはならないため、キャンセルだけを切り離したcontextで実行する（valueは引き継ぐ）。
	hookCtx := context.WithoutCancel(r.Context())
	for _, hook := range h.postHooks {
		if err := hook.OnUserCreated(hookCtx, userID); err != nil {
			slog.Error("post-signup hook failed", "error", err, "userID", userID)
		}
	}
	slog.Info("user signup successful", "email_hash", logging.HashedEmail(req.Email), "remote_addr", httpx.ClientIP(r))
	httpx.WriteJSON(w, http.StatusCreated, api.MessageResponse{Message: "ok"})
}

// Login はユーザーログインAPIエンドポイントを処理します。
// - リクエストJSONをLoginReqにバインド
// - バリデーションエラー時は400を返却
// - 認証失敗時は401を返却
// - 認証成功時はJWTトークン付きで200を返却
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		slog.Warn("login validation failed", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid request"})
		return
	}

	// メールベースのレートリミットチェック
	// 実際のユーザー検索（usecase）と同じ正規化を用い、バケットを一致させる。
	key := fmt.Sprintf("rl:login:email:%s", auth.NormalizeEmail(req.Email))
	result := h.limiter.Allow(r.Context(), key, loginEmailLimit, loginEmailWindow, httpratelimit.FailClosed)
	if !result.Allowed {
		if result.ServiceUnavailable {
			slog.Error("login rate limiter unavailable, rejecting request",
				"type", "email",
				"email_hash", logging.HashedEmail(req.Email),
				"remote_addr", httpx.ClientIP(r),
			)
			httpx.WriteJSON(w, http.StatusServiceUnavailable, api.ErrorResponse{Error: "service temporarily unavailable"})
			return
		}
		slog.Warn("login rate limit exceeded",
			"type", "email",
			"email_hash", logging.HashedEmail(req.Email),
			"remote_addr", httpx.ClientIP(r),
		)
		w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
		httpx.WriteJSON(w, http.StatusTooManyRequests, api.ErrorResponse{Error: "too many requests"})
		return
	}

	// セッション発行後のCSRF生成失敗で利用不能なセッションを残さないよう、先に生成する。
	csrfToken, err := csrf.GenerateToken()
	if err != nil {
		slog.Error("failed to generate csrf token", "error", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal error"})
		return
	}
	pair, err := h.uc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// ユーザー列挙攻撃を防止するため、認証失敗の詳細を公開しない。
			slog.Warn("login failed", "error", err, "email_hash", logging.HashedEmail(req.Email), "remote_addr", httpx.ClientIP(r))
			httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid email or password"})
			return
		}
		if errors.Is(err, auth.ErrSessionUnavailable) {
			slog.Error("login session unavailable", "error", err, "email_hash", logging.HashedEmail(req.Email))
			httpx.WriteJSON(w, http.StatusServiceUnavailable, api.ErrorResponse{Error: "service temporarily unavailable"})
			return
		}
		slog.Error("login failed with internal error", "error", err, "email_hash", logging.HashedEmail(req.Email))
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal error"})
		return
	}

	setSessionCookiesWithCSRF(w, pair, csrfToken, h.secureCookie)

	slog.Info("user login successful", "email_hash", logging.HashedEmail(req.Email), "remote_addr", httpx.ClientIP(r))
	httpx.WriteJSON(w, http.StatusOK, api.MessageResponse{Message: "ok"})
}

// Refresh は有効なリフレッシュトークンを新しいトークンペアへ交換します。
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshTokenCookieName)
	if err != nil || cookie.Value == "" {
		httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid refresh token"})
		return
	}
	// セッションをローテーションした後にCSRF生成が失敗すると、クライアントが
	// 消費済みトークンだけを保持するため、先にCSRFトークンを用意する。
	csrfToken, err := csrf.GenerateToken()
	if err != nil {
		slog.Error("failed to generate csrf token during refresh", "error", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal error"})
		return
	}

	pair, err := h.uc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, auth.ErrRefreshTokenConflict) {
			httpx.WriteJSON(w, http.StatusConflict, api.ErrorResponse{Error: "refresh already in progress"})
			return
		}
		if errors.Is(err, auth.ErrRefreshTokenInvalid) ||
			errors.Is(err, auth.ErrRefreshTokenExpired) ||
			errors.Is(err, auth.ErrRefreshTokenReused) {
			clearSessionCookies(w, h.secureCookie)
			httpx.WriteJSON(w, http.StatusUnauthorized, api.ErrorResponse{Error: "invalid refresh token"})
			return
		}
		if errors.Is(err, auth.ErrSessionUnavailable) {
			slog.Error("refresh session unavailable", "error", err, "remote_addr", httpx.ClientIP(r))
			httpx.WriteJSON(w, http.StatusServiceUnavailable, api.ErrorResponse{Error: "service temporarily unavailable"})
			return
		}
		slog.Error("failed to refresh session", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal error"})
		return
	}
	setSessionCookiesWithCSRF(w, pair, csrfToken, h.secureCookie)
	httpx.WriteJSON(w, http.StatusOK, api.MessageResponse{Message: "ok"})
}

// Logout はリクエストのJWTとリフレッシュセッションを失効させ、認証Cookieを削除します。
// 期限切れトークンでも動作するよう認証不要のルートに配置します。
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken := ""
	if cookie, err := r.Cookie(refreshTokenCookieName); err == nil {
		refreshToken = cookie.Value
	}
	if err := h.uc.Logout(r.Context(), refreshToken); err != nil {
		slog.Error("failed to revoke refresh session on logout", "error", err)
		httpx.WriteJSON(w, http.StatusServiceUnavailable, api.ErrorResponse{Error: "service temporarily unavailable"})
		return
	}

	// トークンが有効な場合のみjtiをブラックリストに登録し、有効期限前でも即時失効させる。
	// Redis未接続時等は警告ログのみでログアウト自体は継続する（グレースフルデグレード）。
	if err := jwt.RevokeRequestToken(r.Context(), r, h.jwtSecret, h.blacklist); err != nil {
		slog.Warn("failed to revoke token on logout", "error", err)
	}

	clearSessionCookies(w, h.secureCookie)

	httpx.WriteJSON(w, http.StatusOK, api.MessageResponse{Message: "ok"})
}

func setSessionCookiesWithCSRF(w http.ResponseWriter, pair auth.TokenPair, csrfToken string, secure bool) {
	setAuthCookie(w, authTokenCookieName, pair.AccessToken, authCookieMaxAge, secure, true)
	setAuthCookie(w, refreshTokenCookieName, pair.RefreshToken, refreshCookieMaxAge, secure, true)
	setAuthCookie(w, csrf.CookieName, csrfToken, refreshCookieMaxAge, secure, false)
}

func clearSessionCookies(w http.ResponseWriter, secure bool) {
	setAuthCookie(w, authTokenCookieName, "", -1, secure, true)
	setAuthCookie(w, refreshTokenCookieName, "", -1, secure, true)
	setAuthCookie(w, csrf.CookieName, "", -1, secure, false)
}

// setAuthCookie は SameSite=Lax の認証関連 Cookie をレスポンスへ設定します。
// 認証関連Cookieの設定・削除に共通利用します。
// maxAge は秒数（削除時は -1）です。
func setAuthCookie(w http.ResponseWriter, name, value string, maxAge int, secure, httpOnly bool) {
	expires := time.Unix(1, 0).UTC()
	if maxAge > 0 {
		expires = time.Now().Add(time.Duration(maxAge) * time.Second).UTC()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  expires,
		Secure:   secure,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	})
}
