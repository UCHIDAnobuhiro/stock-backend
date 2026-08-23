package batch

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/di"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/shared/clientratelimit"
)

// runLogoIngest は TwelveData からロゴURLを取り込み、終了コード（0 or 1）を返す。
func runLogoIngest(cfg *config.Config, sqlDB *sql.DB) int {
	logoProvider := di.NewMarket(cfg.TwelveData)
	symbolRepo := symbollist.NewRepository(sqlDB)
	rateLimiter := clientratelimit.NewRateLimiter(rateLimitPerMinute, time.Minute)
	uc := symbollist.NewLogoIngestUsecase(logoProvider, symbolRepo, rateLimiter)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Batch.LogoTimeoutHours)*time.Hour)
	defer cancel()

	maxFailureRate := cfg.Batch.LogoMaxFailureRate

	start := time.Now()
	result, err := uc.IngestAll(ctx)
	duration := time.Since(start)

	slog.Info("logo ingest summary",
		"total", result.Total,
		"succeeded", result.Succeeded,
		"failed", result.Failed,
		"failure_rate", result.FailureRate(),
		"duration", duration.String(),
	)

	if err != nil {
		slog.Error("logo ingest aborted by fatal error", "error", err)
		return 1
	}
	if shouldFailExit(result, maxFailureRate) {
		slog.Error("logo ingest failure rate exceeded threshold",
			"failure_rate", result.FailureRate(),
			"threshold", maxFailureRate,
		)
		return 1
	}
	slog.Info("logo ingest ok")
	return 0
}
