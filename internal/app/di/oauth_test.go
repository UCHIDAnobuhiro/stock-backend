package di

import (
	"context"
	"database/sql"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
)

// stubOAuthUserStore は OAuthUserStore（UserRepository + OAuthUserCreator）の最小実装。
type stubOAuthUserStore struct{}

func (s *stubOAuthUserStore) Create(ctx context.Context, user *auth.User) error { return nil }
func (s *stubOAuthUserStore) FindByEmail(ctx context.Context, email string) (*auth.User, error) {
	return nil, nil
}
func (s *stubOAuthUserStore) FindByID(ctx context.Context, id int64) (*auth.User, error) {
	return nil, nil
}
func (s *stubOAuthUserStore) CreateUserWithOAuthAccount(ctx context.Context, user *auth.User, account *auth.OAuthAccount) error {
	return nil
}

// stubJWTGenerator は auth.JWTGenerator の最小実装。
type stubJWTGenerator struct{}

func (s *stubJWTGenerator) GenerateToken(userID int64, email string) (string, error) {
	return "", nil
}

func (s *stubJWTGenerator) Issue(context.Context, int64, string) (auth.TokenPair, error) {
	return auth.TokenPair{AccessToken: "access", RefreshToken: "refresh"}, nil
}

// stubUserCreatedHook は auth.UserCreatedHook の最小実装。
type stubUserCreatedHook struct{}

func (s *stubUserCreatedHook) OnUserCreated(ctx context.Context, userID int64) error { return nil }

func TestNewOAuthHandler_RequiresRedis(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		FrontendURL: "http://localhost:3000",
		Google:      &ProviderCredentials{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/cb"},
	}

	h, err := NewOAuthHandler(cfg, nil, nil, &stubOAuthUserStore{}, &stubJWTGenerator{}, &stubUserCreatedHook{}, authhttp.SessionCookieConfig{})
	if err == nil {
		t.Fatal("expected error when Redis is unavailable, got nil")
	}
	if h != nil {
		t.Errorf("expected nil handler on error, got %v", h)
	}
}

func TestNewOAuthHandler_BuildsHandler(t *testing.T) {
	t.Parallel()

	cfg := &OAuthConfig{
		FrontendURL: "http://localhost:3000",
		Google:      &ProviderCredentials{ClientID: "gid", ClientSecret: "gsecret", RedirectURL: "http://localhost/google/cb"},
		GitHub:      &ProviderCredentials{ClientID: "hid", ClientSecret: "hsecret", RedirectURL: "http://localhost/github/cb"},
	}

	// 接続はせず構築のみを検証するため、ダミーの DB / Redis クライアントを渡す。
	db := sql.OpenDB(nil)
	t.Cleanup(func() { _ = db.Close() })
	rdb := redis.NewClient(&redis.Options{})
	t.Cleanup(func() { _ = rdb.Close() })

	h, err := NewOAuthHandler(cfg, db, rdb, &stubOAuthUserStore{}, &stubJWTGenerator{}, &stubUserCreatedHook{}, authhttp.SessionCookieConfig{Secure: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Error("expected non-nil handler")
	}
}
