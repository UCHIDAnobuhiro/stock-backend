package auth

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

// setupTestDB はテスト用の独立した PostgreSQL データベースを準備します。
// 各テストごとに新しい DB が払い出され、t.Cleanup で破棄されます。
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.OpenIsolatedDB(t)
}

// seedUser はテスト用のユーザーをデータベースに作成します。
func seedUser(t *testing.T, db *sql.DB, email, password string) *User {
	t.Helper()
	repo := NewUserRepository(db)
	user := &User{
		Email:    email,
		Password: &password,
	}
	err := repo.Create(context.Background(), user)
	require.NoError(t, err, "failed to seed user")
	return user
}

// ptrStr は文字列のポインタを返すヘルパーです。
func ptrStr(s string) *string { return &s }

func TestNewUserRepository(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepository(db)
	assert.NotNil(t, repo, "repository is nil")
	assert.NotNil(t, repo.db, "database connection is nil")
}

func TestUserRepository_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		user         *User
		wantErr      bool
		expectedErr  error
		setupFunc    func(t *testing.T, db *sql.DB)
		validateFunc func(t *testing.T, user *User)
	}{
		{
			name: "success: user creation",
			user: &User{
				Email:    "test@example.com",
				Password: ptrStr("hashed_password"),
			},
			wantErr: false,
			validateFunc: func(t *testing.T, user *User) {
				assert.NotZero(t, user.ID, "ID is not set")
				assert.False(t, user.CreatedAt.IsZero(), "CreatedAt is not set")
				assert.False(t, user.UpdatedAt.IsZero(), "UpdatedAt is not set")
			},
		},
		{
			name: "failure: duplicate email returns ErrEmailAlreadyExists",
			user: &User{
				Email:    "duplicate@example.com",
				Password: ptrStr("password2"),
			},
			wantErr:     true,
			expectedErr: ErrEmailAlreadyExists,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedUser(t, db, "duplicate@example.com", "password1")
			},
		},
		{
			name:    "failure: nil user",
			user:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewUserRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			err := repo.Create(context.Background(), tt.user)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.expectedErr != nil {
					assert.ErrorIs(t, err, tt.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				if tt.validateFunc != nil {
					tt.validateFunc(t, tt.user)
				}
			}
		})
	}
}

func TestUserRepository_FindByEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		email        string
		wantErr      bool
		expectedErr  error
		setupFunc    func(t *testing.T, db *sql.DB) *User
		validateFunc func(t *testing.T, expected, found *User)
	}{
		{
			name:    "success: find user by email",
			email:   "find@example.com",
			wantErr: false,
			setupFunc: func(t *testing.T, db *sql.DB) *User {
				return seedUser(t, db, "find@example.com", "hashed_password")
			},
			validateFunc: func(t *testing.T, expected, found *User) {
				assert.NotNil(t, found, "user is nil")
				assert.Equal(t, expected.ID, found.ID)
				assert.Equal(t, expected.Email, found.Email)
				assert.Equal(t, expected.Password, found.Password)
			},
		},
		{
			name:        "failure: user not found",
			email:       "notfound@example.com",
			wantErr:     true,
			expectedErr: ErrUserNotFound,
		},
		{
			name:        "failure: empty email",
			email:       "",
			wantErr:     true,
			expectedErr: ErrUserNotFound,
		},
		{
			name:    "success: find correct user when multiple users exist",
			email:   "user2@example.com",
			wantErr: false,
			setupFunc: func(t *testing.T, db *sql.DB) *User {
				seedUser(t, db, "user1@example.com", "pass1")
				user2 := seedUser(t, db, "user2@example.com", "pass2")
				seedUser(t, db, "user3@example.com", "pass3")
				return user2
			},
			validateFunc: func(t *testing.T, expected, found *User) {
				assert.NotNil(t, found, "user is nil")
				assert.Equal(t, expected.ID, found.ID)
				assert.Equal(t, "user2@example.com", found.Email)
				assert.Equal(t, ptrStr("pass2"), found.Password)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewUserRepository(db)
			var expected *User
			if tt.setupFunc != nil {
				expected = tt.setupFunc(t, db)
			}
			found, err := repo.FindByEmail(context.Background(), tt.email)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, found, "user should be nil")
				if tt.expectedErr != nil {
					assert.ErrorIs(t, err, tt.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				if tt.validateFunc != nil {
					tt.validateFunc(t, expected, found)
				}
			}
		})
	}
}

func TestUserRepository_FindByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		wantErr      bool
		expectedErr  error
		setupFunc    func(t *testing.T, db *sql.DB) *User
		validateFunc func(t *testing.T, expected, found *User)
	}{
		{
			name:    "success: find user by ID",
			wantErr: false,
			setupFunc: func(t *testing.T, db *sql.DB) *User {
				return seedUser(t, db, "findbyid@example.com", "hashed_password")
			},
			validateFunc: func(t *testing.T, expected, found *User) {
				assert.NotNil(t, found, "user is nil")
				assert.Equal(t, expected.ID, found.ID)
				assert.Equal(t, expected.Email, found.Email)
				assert.Equal(t, expected.Password, found.Password)
			},
		},
		{
			name:        "failure: user not found",
			userID:      999,
			wantErr:     true,
			expectedErr: ErrUserNotFound,
		},
		{
			name:        "failure: ID 0",
			userID:      0,
			wantErr:     true,
			expectedErr: ErrUserNotFound,
		},
		{
			name:    "success: find correct user when multiple users exist",
			wantErr: false,
			setupFunc: func(t *testing.T, db *sql.DB) *User {
				seedUser(t, db, "user1@example.com", "pass1")
				user2 := seedUser(t, db, "user2@example.com", "pass2")
				seedUser(t, db, "user3@example.com", "pass3")
				return user2
			},
			validateFunc: func(t *testing.T, expected, found *User) {
				assert.NotNil(t, found, "user is nil")
				assert.Equal(t, expected.ID, found.ID)
				assert.Equal(t, "user2@example.com", found.Email)
				assert.Equal(t, ptrStr("pass2"), found.Password)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewUserRepository(db)
			var expected *User
			var targetID int64
			if tt.setupFunc != nil {
				expected = tt.setupFunc(t, db)
				targetID = expected.ID
			} else {
				targetID = tt.userID
			}
			found, err := repo.FindByID(context.Background(), targetID)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, found, "user should be nil")
				if tt.expectedErr != nil {
					assert.ErrorIs(t, err, tt.expectedErr)
				}
			} else {
				assert.NoError(t, err)
				if tt.validateFunc != nil {
					tt.validateFunc(t, expected, found)
				}
			}
		})
	}
}

func TestUserRepository_Timestamps(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewUserRepository(db)

	user := &User{
		Email:    "timestamp@example.com",
		Password: ptrStr("password"),
	}
	err := repo.Create(context.Background(), user)
	require.NoError(t, err)

	assert.False(t, user.CreatedAt.IsZero(), "CreatedAt is not set")
	assert.False(t, user.UpdatedAt.IsZero(), "UpdatedAt is not set")

	found, err := repo.FindByID(context.Background(), user.ID)
	require.NoError(t, err)

	assert.Equal(t, user.CreatedAt.Unix(), found.CreatedAt.Unix())
	assert.Equal(t, user.UpdatedAt.Unix(), found.UpdatedAt.Unix())
}

// TestUserRepository_CreateUserWithOAuthAccount は OAuth 新規ユーザー作成の
// トランザクション動作（成功・User 重複時のロールバック）を検証します。
func TestUserRepository_CreateUserWithOAuthAccount(t *testing.T) {
	t.Parallel()

	t.Run("success: create user and oauth account atomically", func(t *testing.T) {
		t.Parallel()
		db := setupTestDB(t)
		repo := NewUserRepository(db)

		user := &User{Email: "oauth-new@example.com"}
		acct := &OAuthAccount{Provider: "google", ProviderUID: "sub-123"}
		err := repo.CreateUserWithOAuthAccount(context.Background(), user, acct)
		require.NoError(t, err)
		assert.NotZero(t, user.ID)
		assert.False(t, acct.CreatedAt.IsZero())
		assert.Equal(t, user.ID, acct.UserID)
	})

	t.Run("failure: duplicate email rolls back oauth account", func(t *testing.T) {
		t.Parallel()
		db := setupTestDB(t)
		repo := NewUserRepository(db)

		seedUser(t, db, "dup-oauth@example.com", "p")
		user := &User{Email: "dup-oauth@example.com"}
		acct := &OAuthAccount{Provider: "google", ProviderUID: "sub-xyz"}
		err := repo.CreateUserWithOAuthAccount(context.Background(), user, acct)
		assert.ErrorIs(t, err, ErrEmailAlreadyExists)
		assert.Zero(t, user.ID, "user should not be persisted")
		assert.True(t, acct.CreatedAt.IsZero(), "account should not be persisted")
	})
}
