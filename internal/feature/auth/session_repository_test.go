package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshSessionRepository_CreateAndFind(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-create@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	session := makeRefreshSession("session-1", "family-1", user.ID, "token-1", time.Now().Add(time.Hour))

	require.NoError(t, repo.Create(context.Background(), session))
	found, err := repo.FindByTokenHash(context.Background(), hashRefreshToken("token-1"))
	require.NoError(t, err)
	assert.Equal(t, session.ID, found.ID)
	assert.Equal(t, session.FamilyID, found.FamilyID)
	assert.Equal(t, session.UserID, found.UserID)
	assert.Equal(t, session.TokenHash, found.TokenHash)
	assert.Nil(t, found.ConsumedAt)
	assert.Nil(t, found.RevokedAt)
}

func TestRefreshSessionRepository_RotateAndDetectReuse(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-rotate@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	now := time.Now().UTC()
	current := makeRefreshSession("current", "family", user.ID, "current-token", now.Add(time.Hour))
	next := makeRefreshSession("next", "family", user.ID, "next-token", now.Add(2*time.Hour))
	require.NoError(t, repo.Create(context.Background(), current))

	require.NoError(t, repo.Rotate(context.Background(), current.TokenHash, now, fixedRefreshSessionFactory(next)))
	consumed, err := repo.FindByTokenHash(context.Background(), current.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
	require.NotNil(t, consumed.ReplacedBy)
	assert.Equal(t, next.ID, *consumed.ReplacedBy)

	conflict := makeRefreshSession("conflict", "family", user.ID, "conflict-token", now.Add(3*time.Hour))
	err = repo.Rotate(context.Background(), current.TokenHash, now.Add(time.Second), fixedRefreshSessionFactory(conflict))
	assert.ErrorIs(t, err, ErrRefreshTokenConflict)
	active, err := repo.FindByTokenHash(context.Background(), next.TokenHash)
	require.NoError(t, err)
	assert.Nil(t, active.RevokedAt)

	reused := makeRefreshSession("reused", "family", user.ID, "reused-token", now.Add(3*time.Hour))
	err = repo.Rotate(context.Background(), current.TokenHash, now.Add(refreshTokenReuseGracePeriod+time.Second), fixedRefreshSessionFactory(reused))
	assert.ErrorIs(t, err, ErrRefreshTokenReused)
	revoked, err := repo.FindByTokenHash(context.Background(), next.TokenHash)
	require.NoError(t, err)
	assert.NotNil(t, revoked.RevokedAt)
}

func TestRefreshSessionRepository_RotateRejectsExpired(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-expired@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	now := time.Now().UTC()
	current := makeRefreshSession("expired", "family", user.ID, "expired-token", now.Add(-time.Minute))
	next := makeRefreshSession("next", "family", user.ID, "next-token", now.Add(time.Hour))
	require.NoError(t, repo.Create(context.Background(), current))

	err := repo.Rotate(context.Background(), current.TokenHash, now, fixedRefreshSessionFactory(next))
	assert.ErrorIs(t, err, ErrRefreshTokenExpired)
	_, err = repo.FindByTokenHash(context.Background(), next.TokenHash)
	assert.ErrorIs(t, err, ErrRefreshTokenInvalid)
}

func TestRefreshSessionRepository_RotateRejectsDeletedUser(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-deleted-user@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	now := time.Now().UTC()
	current := makeRefreshSession("deleted-user", "family", user.ID, "deleted-user-token", now.Add(time.Hour))
	require.NoError(t, repo.Create(context.Background(), current))

	_, err := db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	require.NoError(t, err)
	factoryCalled := false
	err = repo.Rotate(context.Background(), current.TokenHash, now, func(RefreshSession, string) (*RefreshSession, error) {
		factoryCalled = true
		return nil, nil
	})
	assert.ErrorIs(t, err, ErrRefreshTokenInvalid)
	assert.False(t, factoryCalled)
}

func TestRefreshSessionRepository_ConcurrentRotate(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-concurrent@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	now := time.Now().UTC()
	current := makeRefreshSession("current", "family", user.ID, "current-token", now.Add(time.Hour))
	require.NoError(t, repo.Create(context.Background(), current))

	next := []*RefreshSession{
		makeRefreshSession("next-1", "family", user.ID, "next-token-1", now.Add(time.Hour)),
		makeRefreshSession("next-2", "family", user.ID, "next-token-2", now.Add(time.Hour)),
	}
	errs := make([]error, len(next))
	var wg sync.WaitGroup
	for i := range next {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = repo.Rotate(context.Background(), current.TokenHash, now, fixedRefreshSessionFactory(next[index]))
		}(i)
	}
	wg.Wait()

	successes := 0
	conflicts := 0
	var active *RefreshSession
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRefreshTokenConflict):
			conflicts++
		default:
			t.Fatalf("unexpected rotation error: %v", err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	for i, rotateErr := range errs {
		if rotateErr == nil {
			active = next[i]
			break
		}
	}
	require.NotNil(t, active)
	successor := makeRefreshSession("successor", "family", user.ID, "successor-token", now.Add(2*time.Hour))
	require.NoError(t, repo.Rotate(context.Background(), active.TokenHash, now.Add(time.Second), fixedRefreshSessionFactory(successor)))
}

func TestRefreshSessionRepository_RevokeAndDeleteExpired(t *testing.T) {
	t.Parallel()

	db := setupTestDB(t)
	user := seedUser(t, db, "refresh-revoke@example.com", "hash")
	repo := NewRefreshSessionRepository(db)
	now := time.Now().UTC()
	expired := makeRefreshSession("expired", "family", user.ID, "expired-token", now.Add(-time.Hour))
	active := makeRefreshSession("active", "family", user.ID, "active-token", now.Add(time.Hour))
	require.NoError(t, repo.Create(context.Background(), expired))
	require.NoError(t, repo.Create(context.Background(), active))

	require.NoError(t, repo.Revoke(context.Background(), active.TokenHash, now))
	revoked, err := repo.FindByTokenHash(context.Background(), active.TokenHash)
	require.NoError(t, err)
	assert.NotNil(t, revoked.RevokedAt)

	deleted, err := repo.DeleteExpired(context.Background(), now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	_, err = repo.FindByTokenHash(context.Background(), expired.TokenHash)
	assert.ErrorIs(t, err, ErrRefreshTokenInvalid)
	require.NoError(t, repo.Revoke(context.Background(), hashRefreshToken("unknown"), now))
}

func makeRefreshSession(id, familyID string, userID int64, token string, expiresAt time.Time) *RefreshSession {
	return &RefreshSession{
		ID:        id,
		FamilyID:  familyID,
		UserID:    userID,
		TokenHash: hashRefreshToken(token),
		ExpiresAt: expiresAt,
	}
}

func fixedRefreshSessionFactory(next *RefreshSession) RefreshSessionFactory {
	return func(RefreshSession, string) (*RefreshSession, error) {
		return next, nil
	}
}
