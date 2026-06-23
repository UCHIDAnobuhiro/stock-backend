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

// NewRouter はすべてのアプリケーションルートを設定したHTTPハンドラー（chiルーター）を生成します。
// 公開ルート（signup, login）とJWT認証ミドルウェア付きの保護ルート（candles, symbols, logo, watchlist）を設定します。
// oauthHandler が nil の場合はOAuthルートを登録しません。
func NewRouter(authHandler *authhttp.Handler, oauthHandler *authhttp.OAuthHandler,
	candles *candleshttp.Handler,
	symbol *symbollisthttp.Handler, logo *logodetectionhttp.Handler,
	watchlist *watchlisthttp.Handler,
	limiter *httpratelimit.Limiter,
	openapiValidator func(http.Handler) http.Handler,
	allowedOrigins []string,
	gcpProjectID string,
	jwtSecret string,
) http.Handler {
	r := chi.NewRouter()

	// AccessLog を外側、Recover を内側に置くことで、panic を 500 に変換した結果も
	// アクセスログに記録される。
	r.Use(httpmw.AccessLog(gcpProjectID))
	r.Use(httpmw.Recover())

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Content-Type", "Authorization", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           int((12 * time.Hour).Seconds()),
	}))
	r.Use(httpmw.SecurityHeaders())

	// ヘルスチェックエンドポイント（バージョンなし）。
	// Health はメソッドごとの分岐を自身で行うため、全メソッドを単一ハンドラーで処理する。
	r.Handle("/healthz", http.HandlerFunc(handler.Health))

	// API v1 ルート
	r.Route("/v1", func(r chi.Router) {
		// 公開ルート（認証不要）+ レートリミット + OpenAPI バリデーション。
		// OpenAPI スペックに基づき、パス/クエリ/JSON ボディを契約準拠で検証する。
		r.Group(func(r chi.Router) {
			r.Use(openapiValidator)

			r.With(httpratelimit.ByIP(limiter, httpratelimit.IPRateLimitConfig{
				Prefix: "rl:signup:ip",
				Limit:  5,
				Window: 1 * time.Hour,
			})).Post("/signup", authHandler.Signup)

			r.With(httpratelimit.ByIP(limiter, httpratelimit.IPRateLimitConfig{
				Prefix: "rl:login:ip",
				Limit:  10,
				Window: 1 * time.Minute,
			})).Post("/login", authHandler.Login)

			// 期限切れトークンでもログアウトできるよう認証不要
			r.Delete("/logout", authHandler.Logout)

			// OAuthルート（環境変数が設定されている場合のみ登録）
			if oauthHandler != nil {
				r.Route("/auth/oauth", func(r chi.Router) {
					r.Get("/{provider}", oauthHandler.BeginAuth)
					r.With(httpratelimit.ByIP(limiter, httpratelimit.IPRateLimitConfig{
						Prefix: "rl:oauth:callback:ip",
						Limit:  20,
						Window: 1 * time.Minute,
					})).Get("/{provider}/callback", oauthHandler.Callback)
				})
			}
		})

		// 保護ルート（認証必須・CSRF保護）
		// バリデーションは認証・CSRF の後に行う（未認証/CSRF 不正は 401/403 を優先し、
		// 認証済みリクエストのボディ/パラメータのみ spec 準拠で検証する）。
		r.Group(func(r chi.Router) {
			r.Use(jwt.AuthRequired(jwtSecret))
			r.Use(csrfmw.Protect())
			r.Use(openapiValidator)

			r.Get("/candles/{code}", candles.GetCandlesHandler)
			r.Get("/symbols", symbol.List)
			r.Post("/logo/detect", logo.DetectLogos)
			r.Post("/logo/analyze", logo.AnalyzeCompany)
			r.Get("/watchlist", watchlist.List)
			r.Post("/watchlist", watchlist.Add)
			r.Delete("/watchlist/{code}", watchlist.Remove)
			r.Put("/watchlist/order", watchlist.Reorder)
		})
	})

	return r
}
