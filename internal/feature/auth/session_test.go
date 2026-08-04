package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSessionJWTGenerator struct {
	generateFunc func(int64, string) (string, error)
}

func (m *mockSessionJWTGenerator) GenerateToken(userID int64, email string) (string, error) {
	if m.generateFunc != nil {
		return m.generateFunc(userID, email)
	}
	return "access-token", nil
}

type mockRefreshSessionRepository struct {
	createFunc func(context.Context, *RefreshSession) error
	rotateFunc func(context.Context, []byte, time.Time, RefreshSessionFactory) error
	revokeFunc func(context.Context, []byte, time.Time) error
}

func (m *mockRefreshSessionRepository) Create(ctx context.Context, session *RefreshSession) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, session)
	}
	return nil
}
func (m *mockRefreshSessionRepository) Rotate(ctx context.Context, hash []byte, now time.Time, nextFactory RefreshSessionFactory) error {
	if m.rotateFunc != nil {
		return m.rotateFunc(ctx, hash, now, nextFactory)
	}
	return nil
}
func (m *mockRefreshSessionRepository) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	if m.revokeFunc != nil {
		return m.revokeFunc(ctx, hash, now)
	}
	return nil
}
func TestSessionService_Issue(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var saved *RefreshSession
	repo := &mockRefreshSessionRepository{
		createFunc: func(_ context.Context, session *RefreshSession) error {
			copy := *session
			saved = &copy
			return nil
		},
	}
	service := NewSessionService(&mockSessionJWTGenerator{}, repo, time.Hour)
	service.now = func() time.Time { return fixedNow }

	pair, err := service.Issue(context.Background(), 42, "user@example.com")
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "access-token", pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, fixedNow.Add(time.Hour), pair.RefreshExpiresAt)
	assert.Equal(t, int64(42), saved.UserID)
	assert.NotEmpty(t, saved.ID)
	assert.NotEmpty(t, saved.FamilyID)
	assert.Equal(t, hashRefreshToken(pair.RefreshToken), saved.TokenHash)
	assert.NotEqual(t, []byte(pair.RefreshToken), saved.TokenHash)
}

func TestSessionService_IssueErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		generator JWTGenerator
		repo      RefreshSessionRepository
	}{
		{
			name: "error: access token generation",
			generator: &mockSessionJWTGenerator{generateFunc: func(int64, string) (string, error) {
				return "", errors.New("sign failed")
			}},
			repo: &mockRefreshSessionRepository{},
		},
		{
			name:      "error: refresh session persistence",
			generator: &mockSessionJWTGenerator{},
			repo: &mockRefreshSessionRepository{createFunc: func(context.Context, *RefreshSession) error {
				return errors.New("database unavailable")
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := NewSessionService(tt.generator, tt.repo, time.Hour)
			pair, err := service.Issue(context.Background(), 1, "user@example.com")
			assert.Error(t, err)
			assert.Empty(t, pair)
		})
	}
}

func TestSessionService_Refresh(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var rotated *RefreshSession
	repo := &mockRefreshSessionRepository{
		rotateFunc: func(_ context.Context, hash []byte, now time.Time, nextFactory RefreshSessionFactory) error {
			assert.Equal(t, hashRefreshToken("old-refresh"), hash)
			assert.Equal(t, fixedNow, now)
			next, err := nextFactory(RefreshSession{ID: "current", FamilyID: "family", UserID: 7}, "user@example.com")
			require.NoError(t, err)
			copy := *next
			rotated = &copy
			return nil
		},
	}
	service := NewSessionService(&mockSessionJWTGenerator{}, repo, time.Hour)
	service.now = func() time.Time { return fixedNow }

	pair, err := service.Refresh(context.Background(), "old-refresh")
	require.NoError(t, err)
	require.NotNil(t, rotated)
	assert.Equal(t, "access-token", pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.NotEqual(t, "old-refresh", pair.RefreshToken)
	assert.Equal(t, "family", rotated.FamilyID)
	assert.Equal(t, int64(7), rotated.UserID)
	assert.Equal(t, hashRefreshToken(pair.RefreshToken), rotated.TokenHash)
}

func TestSessionService_RefreshErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		token       string
		generator   JWTGenerator
		repo        RefreshSessionRepository
		want        error
		wantMessage string
	}{
		{
			name:  "error: empty token",
			token: "",
			repo:  &mockRefreshSessionRepository{},
			want:  ErrRefreshTokenInvalid,
		},
		{
			name:  "error: invalid token",
			token: "invalid",
			repo: &mockRefreshSessionRepository{rotateFunc: func(context.Context, []byte, time.Time, RefreshSessionFactory) error {
				return ErrRefreshTokenInvalid
			}},
			want: ErrRefreshTokenInvalid,
		},
		{
			name:  "error: token reuse",
			token: "token",
			repo: &mockRefreshSessionRepository{rotateFunc: func(context.Context, []byte, time.Time, RefreshSessionFactory) error {
				return ErrRefreshTokenReused
			}},
			want: ErrRefreshTokenReused,
		},
		{
			name:  "error: rotation conflict",
			token: "token",
			repo: &mockRefreshSessionRepository{rotateFunc: func(context.Context, []byte, time.Time, RefreshSessionFactory) error {
				return ErrRefreshTokenConflict
			}},
			want: ErrRefreshTokenConflict,
		},
		{
			name:  "error: session store unavailable",
			token: "token",
			repo: &mockRefreshSessionRepository{rotateFunc: func(context.Context, []byte, time.Time, RefreshSessionFactory) error {
				return errors.New("database unavailable")
			}},
			want: ErrSessionUnavailable,
		},
		{
			name:  "error: access token generation",
			token: "token",
			generator: &mockSessionJWTGenerator{generateFunc: func(int64, string) (string, error) {
				return "", errors.New("sign failed")
			}},
			repo: &mockRefreshSessionRepository{rotateFunc: func(_ context.Context, _ []byte, _ time.Time, nextFactory RefreshSessionFactory) error {
				_, err := nextFactory(RefreshSession{FamilyID: "family", UserID: 1}, "user@example.com")
				return err
			}},
			wantMessage: "sign failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			generator := tt.generator
			if generator == nil {
				generator = &mockSessionJWTGenerator{}
			}
			service := NewSessionService(generator, tt.repo, time.Hour)
			pair, err := service.Refresh(context.Background(), tt.token)
			if tt.wantMessage != "" {
				assert.ErrorContains(t, err, tt.wantMessage)
			} else {
				assert.ErrorIs(t, err, tt.want)
			}
			assert.Empty(t, pair)
		})
	}
}

func TestSessionService_Revoke(t *testing.T) {
	t.Parallel()

	var received []byte
	repo := &mockRefreshSessionRepository{
		revokeFunc: func(_ context.Context, hash []byte, _ time.Time) error {
			received = append([]byte(nil), hash...)
			return nil
		},
	}
	service := NewSessionService(&mockSessionJWTGenerator{}, repo, time.Hour)

	require.NoError(t, service.Revoke(context.Background(), "refresh-token"))
	assert.Equal(t, hashRefreshToken("refresh-token"), received)
	require.NoError(t, service.Revoke(context.Background(), ""))
}
