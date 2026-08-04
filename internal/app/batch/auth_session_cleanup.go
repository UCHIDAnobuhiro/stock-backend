package batch

import (
	"context"
	"log/slog"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
)

const authSessionCleanupTimeout = 5 * time.Minute

// runAuthSessionCleanup は期限切れのリフレッシュセッションを削除します。
func runAuthSessionCleanup(cfg *config.Config) int {
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

	ctx, cancel := context.WithTimeout(context.Background(), authSessionCleanupTimeout)
	defer cancel()
	repo := auth.NewRefreshSessionRepository(sqlDB)
	deleted, err := repo.DeleteExpired(ctx, time.Now())
	if err != nil {
		slog.Error("auth session cleanup failed", "error", err)
		return 1
	}
	slog.Info("auth session cleanup ok", "deleted", deleted)
	return 0
}
