// Package server は API サーバーの構成（DI ワイヤリング）と起動・グレースフルシャットダウンを提供します。
// エントリポイントは cmd/api で、設定の読み込みとロガー構成を済ませてから Run を呼びます。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/di"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/router"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/gemini"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/vision"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/symbollisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/watchlisthttp"
	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
	infraredis "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/redis"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/openapivalidate"
)

// Run は依存を組み立てて API サーバーを起動し、終了シグナルを受けるまでブロックして終了コードを返す。
// 外部接続や起動の失敗は 1、正常終了は 0。環境変数から読み込んだ設定は cfg として注入される。
// os.Exit は呼ばず、終了コードを返すのみ（呼び出し側の main で os.Exit する）。
func Run(cfg *config.Config) int {
	// Redis接続。PING 1回で失敗が判明するため、リトライ付きで待つ DB より先に確立する。
	rdb, err := infraredis.NewRedisClient(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password)
	if err != nil {
		slog.Error("Redis startup failed", "error", err)
		return 1
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			slog.Error("Failed to close Redis client", "error", err)
		}
	}()

	// データベース接続。スキーマ適用は cmd/migrate バイナリ（goose）で別途実施する。
	sqlDB, err := infradb.OpenSQL(cfg.DB)
	if err != nil {
		slog.Error("DB open failed", "error", err)
		return 1
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("failed to close sqlDB", "error", err)
		}
	}()

	// 全 feature が sqlc 化済み。
	userRepo := auth.NewUserRepository(sqlDB)
	symbolRepo := symbollist.NewRepository(sqlDB)
	candleRepo := candles.NewRepository(sqlDB)
	watchlistRepo := watchlist.NewRepository(sqlDB)

	// Redisキャッシュでラップ（ingestのUpsertBatchは対象キーをDELするのみで、再構築は
	// 次回Findのcache-miss時に行われる。TTLはDEL失敗時や競合による汚染時のセーフティネット）
	cachedCandleRepo := candles.NewCachingRepository(rdb, cfg.Cache.CandlesTTL, candleRepo, "candles")

	// JWTジェネレータ・ブラックリスト（ログアウト時の即時失効用）
	jwtGen := jwt.NewGenerator(cfg.Server.JWTSecret, jwt.DefaultTokenTTL)
	jwtBlacklist := jwt.NewBlacklist(rdb)
	refreshSessionRepo := auth.NewRefreshSessionRepository(sqlDB)
	sessionService := auth.NewSessionService(jwtGen, refreshSessionRepo, auth.DefaultRefreshTokenTTL)

	// Google Cloudクライアント初期化
	visionDetector, err := vision.NewVisionLogoDetector(context.Background())
	if err != nil {
		slog.Error("failed to create vision client", "error", err)
		return 1
	}
	defer func() {
		if err := visionDetector.Close(); err != nil {
			slog.Error("Failed to close vision client", "error", err)
		}
	}()

	geminiAnalyzer, err := gemini.NewGeminiAnalyzer(context.Background())
	if err != nil {
		slog.Error("failed to create gemini client", "error", err)
		return 1
	}

	// レートリミッター
	rateLimiter := httpratelimit.NewLimiter(rdb)

	// ユースケース
	authUC := auth.NewUsecase(userRepo, sessionService, cfg.Server.PasswordPepper)
	symbolUC := symbollist.NewUsecase(symbolRepo)
	candlesUC := candles.NewUsecase(cachedCandleRepo)
	logoUC := logodetection.NewUsecase(visionDetector, geminiAnalyzer)
	watchlistUC := watchlist.NewUsecase(watchlistRepo, symbolRepo)
	sessionCookies := authhttp.SessionCookieConfig{
		Secure: cfg.Server.SecureCookie,
		Domain: cfg.Server.CookieDomain,
	}

	// OAuth ハンドラー（cfg.OAuth が nil の場合はOAuth機能なしで起動）
	var oauthH *authhttp.OAuthHandler
	if cfg.OAuth != nil {
		var err error
		oauthH, err = di.NewOAuthHandler(cfg.OAuth, sqlDB, rdb, userRepo, sessionService, watchlistUC, sessionCookies)
		if err != nil {
			slog.Error("failed to set up OAuth", "error", err)
			return 1
		}
	}

	// ハンドラー
	authH := authhttp.NewHandler(authUC, rateLimiter, sessionCookies, cfg.Server.JWTSecret, jwtBlacklist, watchlistUC)
	symbolH := symbollisthttp.NewHandler(symbolUC)
	candlesH := candleshttp.NewHandler(candlesUC)
	logoH := logodetectionhttp.NewHandler(logoUC)
	watchlistH := watchlisthttp.NewHandler(watchlistUC)

	// OpenAPI スペックに基づくリクエストバリデーションミドルウェア
	openapiValidator, err := openapivalidate.New()
	if err != nil {
		slog.Error("failed to set up OpenAPI request validator", "error", err)
		return 1
	}

	// ルーター作成
	r := router.NewRouter(
		router.Handlers{
			Auth: authH, OAuth: oauthH, Candles: candlesH,
			Symbol: symbolH, Logo: logoH, Watchlist: watchlistH,
		},
		router.Config{
			Limiter:          rateLimiter,
			OpenAPIValidator: openapiValidator,
			AllowedOrigins:   cfg.Server.CORSOrigins,
			JWTSecret:        cfg.Server.JWTSecret,
			Blacklist:        jwtBlacklist,
			SecureCookie:     cfg.Server.SecureCookie,
			TrustedProxyHops: cfg.Server.TrustedProxyHops,
		},
	)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// SIGINT / SIGTERM を受けてグレースフルシャットダウンする。
	// Cloud Run 等では SIGTERM 受信後に処理中リクエストを完了させてから終了する。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Starting server", "port", 8080)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			slog.Error("Server failed to start", "error", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		slog.Info("Shutdown signal received, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
			return 1
		}
		slog.Info("Server stopped gracefully")
		return 0
	}
}
