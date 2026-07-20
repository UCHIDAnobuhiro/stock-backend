package batch

import (
	"context"
	"log/slog"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/di"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
	infraredis "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/redis"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/shared/clientratelimit"
)

// runCandleIngest は TwelveData から株価データを取り込み、終了コード（0 or 1）を返す。
func runCandleIngest(cfg *config.Config) int {
	sqlDB, err := db.OpenSQL(cfg.DB)
	if err != nil {
		slog.Error("DB open failed", "error", err)
		return 1
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("failed to close sqlDB", "error", err)
		}
	}()
	marketRepo := di.NewMarket(cfg.TwelveData)
	candleRepo := candles.NewRepository(sqlDB)
	symbolRepo := symbollist.NewRepository(sqlDB)
	ingestSymbolRepo := di.NewIngestSymbolAdapter(symbolRepo)
	rateLimiter := clientratelimit.NewRateLimiter(rateLimitPerMinute, time.Minute)

	// Redis接続（ベストエフォート: 接続失敗時はキャッシュ削除なしで続行）
	var rdb *redisv9.Client
	if tmp, err := infraredis.NewRedisClient(cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password); err != nil {
		slog.Warn("Redis unavailable, cache invalidation disabled", "error", err)
	} else {
		rdb = tmp
		defer func() {
			if err := rdb.Close(); err != nil {
				slog.Error("Failed to close Redis client", "error", err)
			}
		}()
	}

	// UpsertBatchは対象キーのキャッシュをDELするのみで、再構築は次回Findのcache-miss時に行われる。
	// TTLはDEL失敗時や競合による汚染時に古いキャッシュが残り続けないためのセーフティネット。
	cachedCandleRepo := candles.NewCachingRepository(rdb, candles.DefaultCacheTTL, candleRepo, "candles")

	uc := candles.NewIngestUsecase(marketRepo, cachedCandleRepo, ingestSymbolRepo, rateLimiter)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Batch.CandlesTimeoutHours)*time.Hour)
	defer cancel()

	maxFailureRate := cfg.Batch.CandlesMaxFailureRate

	start := time.Now()
	result, err := uc.IngestAll(ctx)
	duration := time.Since(start)

	slog.Info("ingest summary",
		"total", result.Total,
		"succeeded", result.Succeeded,
		"failed", result.Failed,
		"failure_rate", result.FailureRate(),
		"duration", duration.String(),
	)

	if err != nil {
		slog.Error("ingest aborted by fatal error", "error", err)
		return 1
	}
	if shouldFailExit(result, maxFailureRate) {
		slog.Error("ingest failure rate exceeded threshold",
			"failure_rate", result.FailureRate(),
			"threshold", maxFailureRate,
		)
		return 1
	}
	slog.Info("ingest ok")
	return 0
}
