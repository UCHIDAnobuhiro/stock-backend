package batch

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
)

const authSessionCleanupTimeout = 5 * time.Minute

// runAuthSessionCleanup は期限切れのリフレッシュセッションを削除します。
func runAuthSessionCleanup(_ *config.Config, sqlDB *sql.DB) int {
	ctx, cancel := context.WithTimeout(context.Background(), authSessionCleanupTimeout)
	defer cancel()
	repo := auth.NewRefreshSessionRepository(sqlDB)
	uc := auth.NewSessionCleanupUsecase(repo)
	deleted, err := uc.CleanupExpired(ctx)
	if err != nil {
		slog.Error("auth session cleanup failed", "error", err)
		return 1
	}
	slog.Info("auth session cleanup ok", "deleted", deleted)
	return 0
}
