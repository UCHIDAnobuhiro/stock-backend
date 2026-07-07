package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

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
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
	infraredis "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/redis"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/openapivalidate"
)

// main は run の戻り値で os.Exit するだけのラッパー。
// os.Exit は defer を実行しないため、DB / Redis / Vision クライアントの
// Close 等の後処理が走るよう実体は run に分離している。
func main() {
	os.Exit(run())
}

// run は API サーバーを構成・起動し、終了コードを返す。
// 設定不正は 2、外部接続や起動の失敗は 1、正常終了は 0。
func run() int {
	// 環境変数を一括で読み込み・検証する（os.Getenv の呼び出しは config に集約）。
	cfg, err := config.LoadAPI()
	// ロガーは設定読み込みの成否に関わらず構成する（cfg.Log は best-effort で埋まる）。
	logger := slog.New(logging.NewHandler(os.Stdout, cfg.Log.Level, cfg.Log.UseJSON))
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings {
		slog.Warn(w)
	}
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return 2
	}

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

	// Redis接続
	var rdb *redisv9.Client
	if tmp, err := infraredis.NewRedisClient(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password); err != nil {
		slog.Warn("Redis unavailable, running without cache", "error", err)
		rdb = nil
	} else {
		rdb = tmp
		defer func() {
			if err := rdb.Close(); err != nil {
				slog.Error("Failed to close Redis client", "error", err)
			}
		}()
	}

	// 全 feature が sqlc 化済み。
	userRepo := auth.NewUserRepository(sqlDB)
	symbolRepo := symbollist.NewRepository(sqlDB)
	candleRepo := candles.NewRepository(sqlDB)
	watchlistRepo := watchlist.NewRepository(sqlDB)

	// Redisキャッシュでラップ（TTLはingest連続失敗時のセーフティネット、通常は日次ingestで上書き）
	cachedCandleRepo := candles.NewCachingRepository(rdb, candles.DefaultCacheTTL, candleRepo, "candles")

	// JWTジェネレータ・ブラックリスト（ログアウト時の即時失効用）
	jwtGen := jwt.NewGenerator(cfg.Server.JWTSecret, 1*time.Hour)
	jwtBlacklist := jwt.NewBlacklist(rdb)

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
	authUC := auth.NewUsecase(userRepo, jwtGen, cfg.Server.PasswordPepper)
	symbolUC := symbollist.NewUsecase(symbolRepo)
	candlesUC := candles.NewUsecase(cachedCandleRepo)
	logoUC := logodetection.NewUsecase(visionDetector, geminiAnalyzer)
	watchlistUC := watchlist.NewUsecase(watchlistRepo, symbolRepo)

	// OAuth ハンドラー（cfg.OAuth が nil の場合はOAuth機能なしで起動）
	var oauthH *authhttp.OAuthHandler
	if cfg.OAuth != nil {
		oauthH, err = di.NewOAuthHandler(cfg.OAuth, sqlDB, rdb, userRepo, jwtGen, watchlistUC, cfg.Server.SecureCookie)
		if err != nil {
			slog.Error("failed to set up OAuth", "error", err)
			return 1
		}
	}

	// ハンドラー
	authH := authhttp.NewHandler(authUC, rateLimiter, cfg.Server.SecureCookie, cfg.Server.JWTSecret, jwtBlacklist, watchlistUC)
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
			GCPProjectID:     cfg.Server.GCPProjectID,
			JWTSecret:        cfg.Server.JWTSecret,
			Blacklist:        jwtBlacklist,
			SecureCookie:     cfg.Server.SecureCookie,
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
