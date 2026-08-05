package auth

import (
	"context"
	"fmt"
	"time"
)

// ExpiredSessionRepository は期限切れリフレッシュセッションの削除操作を定義します。
type ExpiredSessionRepository interface {
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// sessionCleanupUsecase は期限切れリフレッシュセッションの削除を扱います。
type sessionCleanupUsecase struct {
	sessions ExpiredSessionRepository
	now      func() time.Time
}

// NewSessionCleanupUsecase は認証セッション削除ユースケースを生成します。
func NewSessionCleanupUsecase(sessions ExpiredSessionRepository) *sessionCleanupUsecase {
	return &sessionCleanupUsecase{
		sessions: sessions,
		now:      time.Now,
	}
}

// CleanupExpired は現在時刻より前に期限切れとなったセッションを削除します。
func (u *sessionCleanupUsecase) CleanupExpired(ctx context.Context) (int64, error) {
	deleted, err := u.sessions.DeleteExpired(ctx, u.now())
	if err != nil {
		return 0, fmt.Errorf("delete expired refresh sessions: %w", err)
	}
	return deleted, nil
}
