// Package router はアプリケーションのHTTPルーティングを設定します。
package router

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/symbollisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/watchlisthttp"
	csrfmw "github.com/UCHIDAnobuhiro/stock-backend/internal/transport/csrf"
	handler "github.com/UCHIDAnobuhiro/stock-backend/internal/transport/handler"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
	httpmw "github.com/UCHIDAnobuhiro/stock-backend/internal/transport/middleware"
)

// Handlers は各フィーチャーのHTTPハンドラーをまとめる。
type Handlers struct {
	Auth      *authhttp.Handler
	OAuth     *authhttp.OAuthHandler // nil ならOAuthルート未登録
	Candles   *candleshttp.Handler
	Symbol    *symbollisthttp.Handler
	Logo      *logodetectionhttp.Handler
	Watchlist *watchlisthttp.Handler
}

// Config はルーター構築に必要な設定値・横断部品。
type Config struct {
	Limiter          *httpratelimit.Limiter
	OpenAPIValidator func(http.Handler) http.Handler
	AllowedOrigins   []string
	GCPProjectID     string
	JWTSecret        string
	// SecureCookie が true（本番・TLS終端）のとき HSTS ヘッダーを有効化する。
	SecureCookie bool
}

// NewRouter はすべてのアプリケーションルートを設定したHTTPハンドラー（chiルーター）を生成します。
// 公開ルート（signup, login）とJWT認証ミドルウェア付きの保護ルート（candles, symbols, logo, watchlist）を設定します。
// h.OAuth が nil の場合はOAuthルートを登録しません。
func NewRouter(h Handlers, cfg Config) http.Handler {
	r := chi.NewRouter()

	// AccessLog を外側、Recover を内側に置くことで、panic を 500 に変換した結果も
	// アクセスログに記録される。
	r.Use(httpmw.AccessLog(cfg.GCPProjectID))
	r.Use(httpmw.Recover())

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           int((12 * time.Hour).Seconds()),
	}))
	r.Use(httpmw.SecurityHeaders(cfg.SecureCookie))

	// ヘルスチェックエンドポイント（バージョンなし）。
	// Health はメソッドごとの分岐を自身で行うため、全メソッドを単一ハンドラーで処理する。
	r.Handle("/healthz", http.HandlerFunc(handler.Health))

	// API v1 ルート
	r.Route("/v1", func(r chi.Router) {
		// 公開ルート（認証不要）+ レートリミット + OpenAPI バリデーション。
		// OpenAPI スペックに基づき、パス/クエリ/JSON ボディを契約準拠で検証する。
		r.Group(func(r chi.Router) {
			r.Use(cfg.OpenAPIValidator)

			r.With(httpratelimit.ByIP(cfg.Limiter, httpratelimit.IPRateLimitConfig{
				Prefix: "rl:signup:ip",
				Limit:  5,
				Window: 1 * time.Hour,
			})).Post("/signup", h.Auth.Signup)

			r.With(httpratelimit.ByIP(cfg.Limiter, httpratelimit.IPRateLimitConfig{
				Prefix: "rl:login:ip",
				Limit:  10,
				Window: 1 * time.Minute,
			})).Post("/login", h.Auth.Login)

			// 期限切れトークンでもログアウトできるよう認証不要
			r.Delete("/logout", h.Auth.Logout)

			// OAuthルート（環境変数が設定されている場合のみ登録）
			if h.OAuth != nil {
				r.Route("/auth/oauth", func(r chi.Router) {
					r.Get("/{provider}", h.OAuth.BeginAuth)
					r.With(httpratelimit.ByIP(cfg.Limiter, httpratelimit.IPRateLimitConfig{
						Prefix: "rl:oauth:callback:ip",
						Limit:  20,
						Window: 1 * time.Minute,
					})).Get("/{provider}/callback", h.OAuth.Callback)
				})
			}
		})

		// 保護ルート（認証必須・CSRF保護）
		// バリデーションは認証・CSRF の後に行う（未認証/CSRF 不正は 401/403 を優先し、
		// 認証済みリクエストのボディ/パラメータのみ spec 準拠で検証する）。
		r.Group(func(r chi.Router) {
			r.Use(jwt.AuthRequired(cfg.JWTSecret))
			r.Use(csrfmw.Protect())
			r.Use(cfg.OpenAPIValidator)

			r.Get("/candles/{code}", h.Candles.GetCandlesHandler)
			r.Get("/symbols", h.Symbol.List)

			// Gemini/Vision API のコスト制御のため、logo 系エンドポイント合算で
			// IP別・ユーザー別それぞれ 10回/日 に制限する。
			// IP別はアカウント切り替えによる回避、ユーザー別はIP変更による回避を防ぐ。
			logoIPRateLimit := httpratelimit.ByIP(cfg.Limiter, httpratelimit.IPRateLimitConfig{
				Prefix: "rl:logo:ip",
				Limit:  10,
				Window: 24 * time.Hour,
			})
			logoUserRateLimit := httpratelimit.ByUser(cfg.Limiter, httpratelimit.UserRateLimitConfig{
				Prefix: "rl:logo:user",
				Limit:  10,
				Window: 24 * time.Hour,
			})
			r.With(logoIPRateLimit, logoUserRateLimit).Post("/logo/detect", h.Logo.DetectLogos)
			r.With(logoIPRateLimit, logoUserRateLimit).Post("/logo/analyze", h.Logo.AnalyzeCompany)
			r.Get("/watchlist", h.Watchlist.List)
			r.Post("/watchlist", h.Watchlist.Add)
			r.Delete("/watchlist/{code}", h.Watchlist.Remove)
			r.Put("/watchlist/order", h.Watchlist.Reorder)
		})
	})

	return r
}
