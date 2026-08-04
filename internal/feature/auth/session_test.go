package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSessionUserRepository struct {
	findByIDFunc func(context.Context, int64) (*User, error)
}

func (m *mockSessionUserRepository) Create(context.Context, *User) error { return nil }
func (m *mockSessionUserRepository) FindByEmail(context.Context, string) (*User, error) {
	return nil, ErrUserNotFound
}
func (m *mockSessionUserRepository) FindByID(ctx context.Context, id int64) (*User, error) {
	if m.findByIDFunc != nil {
		return m.findByIDFunc(ctx, id)
	}
	return &User{ID: id, Email: "user@example.com"}, nil
}

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
	findFunc   func(context.Context, []byte) (*RefreshSession, error)
	rotateFunc func(context.Context, []byte, *RefreshSession, time.Time) error
	revokeFunc func(context.Context, []byte, time.Time) error
}

func (m *mockRefreshSessionRepository) Create(ctx context.Context, session *RefreshSession) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, session)
	}
	return nil
}
func (m *mockRefreshSessionRepository) FindByTokenHash(ctx context.Context, hash []byte) (*RefreshSession, error) {
	if m.findFunc != nil {
		return m.findFunc(ctx, hash)
	}
	return nil, ErrRefreshTokenInvalid
}
func (m *mockRefreshSessionRepository) Rotate(ctx context.Context, hash []byte, next *RefreshSession, now time.Time) error {
	if m.rotateFunc != nil {
		return m.rotateFunc(ctx, hash, next, now)
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
	service := NewSessionService(&mockSessionUserRepository{}, &mockSessionJWTGenerator{}, repo, time.Hour)
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
			service := NewSessionService(&mockSessionUserRepository{}, tt.generator, tt.repo, time.Hour)
			pair, err := service.Issue(context.Background(), 1, "user@example.com")
			assert.Error(t, err)
			assert.Empty(t, pair)
		})
	}
}

func TestSessionService_Refresh(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	current := &RefreshSession{ID: "current", FamilyID: "family", UserID: 7}
	var rotated *RefreshSession
	repo := &mockRefreshSessionRepository{
		findFunc: func(_ context.Context, hash []byte) (*RefreshSession, error) {
			assert.Equal(t, hashRefreshToken("old-refresh"), hash)
			return current, nil
		},
		rotateFunc: func(_ context.Context, hash []byte, next *RefreshSession, now time.Time) error {
			assert.Equal(t, hashRefreshToken("old-refresh"), hash)
			assert.Equal(t, fixedNow, now)
			copy := *next
			rotated = &copy
			return nil
		},
	}
	service := NewSessionService(&mockSessionUserRepository{}, &mockSessionJWTGenerator{}, repo, time.Hour)
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
		name  string
		token string
		users UserRepository
		repo  RefreshSessionRepository
		want  error
	}{
		{
			name:  "error: empty token",
			token: "",
			users: &mockSessionUserRepository{},
			repo:  &mockRefreshSessionRepository{},
			want:  ErrRefreshTokenInvalid,
		},
		{
			name:  "error: token lookup",
			token: "invalid",
			users: &mockSessionUserRepository{},
			repo: &mockRefreshSessionRepository{findFunc: func(context.Context, []byte) (*RefreshSession, error) {
				return nil, ErrRefreshTokenInvalid
			}},
			want: ErrRefreshTokenInvalid,
		},
		{
			name:  "error: user lookup",
			token: "token",
			users: &mockSessionUserRepository{findByIDFunc: func(context.Context, int64) (*User, error) {
				return nil, ErrUserNotFound
			}},
			repo: &mockRefreshSessionRepository{findFunc: func(context.Context, []byte) (*RefreshSession, error) {
				return &RefreshSession{UserID: 1}, nil
			}},
			want: ErrSessionUnavailable,
		},
		{
			name:  "error: token reuse",
			token: "token",
			users: &mockSessionUserRepository{},
			repo: &mockRefreshSessionRepository{
				findFunc: func(context.Context, []byte) (*RefreshSession, error) {
					return &RefreshSession{FamilyID: "family", UserID: 1}, nil
				},
				rotateFunc: func(context.Context, []byte, *RefreshSession, time.Time) error {
					return ErrRefreshTokenReused
				},
			},
			want: ErrRefreshTokenReused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := NewSessionService(tt.users, &mockSessionJWTGenerator{}, tt.repo, time.Hour)
			pair, err := service.Refresh(context.Background(), tt.token)
			assert.ErrorIs(t, err, tt.want)
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
	service := NewSessionService(&mockSessionUserRepository{}, &mockSessionJWTGenerator{}, repo, time.Hour)

	require.NoError(t, service.Revoke(context.Background(), "refresh-token"))
	assert.Equal(t, hashRefreshToken("refresh-token"), received)
	require.NoError(t, service.Revoke(context.Background(), ""))
}
