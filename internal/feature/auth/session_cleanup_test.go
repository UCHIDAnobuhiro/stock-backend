package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockExpiredSessionRepository struct {
	deleteExpiredFunc  func(context.Context, time.Time) (int64, error)
	deleteExpiredCalls int
}

func (m *mockExpiredSessionRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	m.deleteExpiredCalls++
	if m.deleteExpiredFunc != nil {
		return m.deleteExpiredFunc(ctx, before)
	}
	return 0, nil
}

func TestSessionCleanupUsecase_CleanupExpired(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("database unavailable")
	tests := []struct {
		name        string
		deleted     int64
		repoErr     error
		wantDeleted int64
		wantErr     error
	}{
		{
			name:        "success: returns deleted count",
			deleted:     12,
			wantDeleted: 12,
		},
		{
			name:    "error: wraps repository failure",
			repoErr: repositoryErr,
			wantErr: repositoryErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var receivedBefore time.Time
			repo := &mockExpiredSessionRepository{
				deleteExpiredFunc: func(_ context.Context, before time.Time) (int64, error) {
					receivedBefore = before
					return tt.deleted, tt.repoErr
				},
			}
			uc := NewSessionCleanupUsecase(repo)
			uc.now = func() time.Time { return fixedNow }

			deleted, err := uc.CleanupExpired(context.Background())

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantDeleted, deleted)
			assert.Equal(t, fixedNow, receivedBefore)
			assert.Equal(t, 1, repo.deleteExpiredCalls)
		})
	}
}
