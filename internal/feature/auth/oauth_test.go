package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
)

// mockOAuthProvider は OAuthProvider インターフェースのモック実装です。
type mockOAuthProvider struct {
	ExchangeCodeFunc func(ctx context.Context, code, codeVerifier string) (*auth.OAuthUserInfo, error)
}

func (m *mockOAuthProvider) AuthorizationURL(state, codeChallenge string) string { return "" }

func (m *mockOAuthProvider) ExchangeCode(ctx context.Context, code, codeVerifier string) (*auth.OAuthUserInfo, error) {
	return m.ExchangeCodeFunc(ctx, code, codeVerifier)
}

// mockOAuthStateStore は OAuthStateStore インターフェースのモック実装です。
type mockOAuthStateStore struct {
	ConsumeStateFunc func(ctx context.Context, state string) (string, error)
}

func (m *mockOAuthStateStore) SaveState(ctx context.Context, state, codeVerifier string, ttl time.Duration) error {
	return nil
}

func (m *mockOAuthStateStore) ConsumeState(ctx context.Context, state string) (string, error) {
	return m.ConsumeStateFunc(ctx, state)
}

// mockOAuthAccountRepository は OAuthAccountRepository インターフェースのモック実装です。
type mockOAuthAccountRepository struct {
	FindByProviderFunc func(ctx context.Context, provider, providerUID string) (*auth.OAuthAccount, error)
	CreateFunc         func(ctx context.Context, account *auth.OAuthAccount) error
}

func (m *mockOAuthAccountRepository) FindByProvider(ctx context.Context, provider, providerUID string) (*auth.OAuthAccount, error) {
	return m.FindByProviderFunc(ctx, provider, providerUID)
}

func (m *mockOAuthAccountRepository) Create(ctx context.Context, account *auth.OAuthAccount) error {
	return m.CreateFunc(ctx, account)
}

// mockOAuthUserCreator は OAuthUserCreator インターフェースのモック実装です。
type mockOAuthUserCreator struct {
	CreateUserWithOAuthAccountFunc func(ctx context.Context, user *auth.User, account *auth.OAuthAccount) error
}

func (m *mockOAuthUserCreator) CreateUserWithOAuthAccount(ctx context.Context, user *auth.User, account *auth.OAuthAccount) error {
	return m.CreateUserWithOAuthAccountFunc(ctx, user, account)
}

// TestOAuthUsecase_HandleCallback_NormalizesEmailForAutoLink は、プロバイダーが
// 既存ユーザーと大小文字違いのメールを返した場合でも、正規化済みメールで検索され
// 既存ユーザーへ自動リンクされる（新規作成されない）ことを検証します。
func TestOAuthUsecase_HandleCallback_NormalizesEmailForAutoLink(t *testing.T) {
	t.Parallel()

	const providerName = "google"
	existingUser := &auth.User{ID: 42, Email: "user@example.com"}

	var lookupEmail string
	var linkedUserID int64
	creatorCalled := false

	users := &mockUserRepository{
		FindByEmailFunc: func(ctx context.Context, email string) (*auth.User, error) {
			lookupEmail = email
			// 正規化済みメールで一致する既存ユーザーを返す
			if email == existingUser.Email {
				return existingUser, nil
			}
			return nil, auth.ErrUserNotFound
		},
	}
	oauthAccts := &mockOAuthAccountRepository{
		FindByProviderFunc: func(ctx context.Context, provider, providerUID string) (*auth.OAuthAccount, error) {
			// 既存 OAuthAccount なし
			return nil, auth.ErrUserNotFound
		},
		CreateFunc: func(ctx context.Context, account *auth.OAuthAccount) error {
			linkedUserID = account.UserID
			return nil
		},
	}
	creator := &mockOAuthUserCreator{
		CreateUserWithOAuthAccountFunc: func(ctx context.Context, user *auth.User, account *auth.OAuthAccount) error {
			creatorCalled = true
			return nil
		},
	}
	stateStore := &mockOAuthStateStore{
		ConsumeStateFunc: func(ctx context.Context, state string) (string, error) {
			return "code-verifier", nil
		},
	}
	provider := &mockOAuthProvider{
		ExchangeCodeFunc: func(ctx context.Context, code, codeVerifier string) (*auth.OAuthUserInfo, error) {
			// プロバイダーは大小文字・空白違いのメールを返す
			return &auth.OAuthUserInfo{ProviderUID: "google-uid-1", Email: "  User@Example.COM "}, nil
		},
	}

	uc := auth.NewOAuthUsecase(
		users, oauthAccts, creator, stateStore,
		&mockJWTGenerator{},
		map[string]auth.OAuthProvider{providerName: provider},
	)

	token, err := uc.HandleCallback(context.Background(), providerName, "code", "state")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("token is empty")
	}
	if lookupEmail != existingUser.Email {
		t.Errorf("FindByEmail called with %q, want %q", lookupEmail, existingUser.Email)
	}
	if creatorCalled {
		t.Error("expected auto-link to existing user, but a new user was created")
	}
	if linkedUserID != existingUser.ID {
		t.Errorf("linked OAuthAccount.UserID = %d, want %d", linkedUserID, existingUser.ID)
	}
}
