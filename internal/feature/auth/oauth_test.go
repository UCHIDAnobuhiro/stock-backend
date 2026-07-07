package auth_test

import (
	"context"
	"errors"
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

// TestOAuthUsecase_HandleCallback_FindOrCreateUser は、OAuthAccount の有無と
// 同メール既存ユーザーの有無に応じたログイン・拒否・新規作成の分岐を検証します。
// 同メールの既存ユーザーへの自動リンクはアカウント乗っ取りリスクがあるため、
// いかなる場合も OAuthAccount の自動リンク（Create）が行われないことを確認します。
func TestOAuthUsecase_HandleCallback_FindOrCreateUser(t *testing.T) {
	t.Parallel()

	const providerName = "google"
	password := "hashed-password"

	tests := []struct {
		name              string
		providerEmail     string // プロバイダーが返すメール
		existingAccount   *auth.OAuthAccount
		existingUser      *auth.User // 正規化済みメールで見つかる既存ユーザー（nilなら未検出）
		creatorErr        error
		wantErr           error
		wantCreatorCalled bool
		wantLookupEmail   string // FindByEmail に渡される正規化済みメール（空なら未検証）
	}{
		{
			name:            "既存OAuthAccountあり: リンクせずログイン成功",
			providerEmail:   "user@example.com",
			existingAccount: &auth.OAuthAccount{ID: 1, UserID: 42, Provider: providerName, ProviderUID: "google-uid-1"},
		},
		{
			name:          "同メールのパスワードユーザーあり: 自動リンクを拒否",
			providerEmail: "user@example.com",
			existingUser:  &auth.User{ID: 42, Email: "user@example.com", Password: &password},
			wantErr:       auth.ErrOAuthEmailConflict,
		},
		{
			name:          "同メールのOAuth専用ユーザーあり（別プロバイダー登録）: 自動リンクを拒否",
			providerEmail: "user@example.com",
			existingUser:  &auth.User{ID: 42, Email: "user@example.com", Password: nil},
			wantErr:       auth.ErrOAuthEmailConflict,
		},
		{
			name:              "既存ユーザーなし: 新規ユーザーを作成",
			providerEmail:     "user@example.com",
			wantCreatorCalled: true,
		},
		{
			name:            "正規化済みメールで検索され拒否される（大小文字・空白違い）",
			providerEmail:   "  User@Example.COM ",
			existingUser:    &auth.User{ID: 42, Email: "user@example.com", Password: &password},
			wantErr:         auth.ErrOAuthEmailConflict,
			wantLookupEmail: "user@example.com",
		},
		{
			name:              "作成時の並行レース（emailユニーク制約違反）: 拒否として扱う",
			providerEmail:     "user@example.com",
			creatorErr:        auth.ErrEmailAlreadyExists,
			wantErr:           auth.ErrOAuthEmailConflict,
			wantCreatorCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var lookupEmail string
			linkCalled := false
			creatorCalled := false

			users := &mockUserRepository{
				FindByEmailFunc: func(ctx context.Context, email string) (*auth.User, error) {
					lookupEmail = email
					if tt.existingUser != nil && email == tt.existingUser.Email {
						return tt.existingUser, nil
					}
					return nil, auth.ErrUserNotFound
				},
			}
			oauthAccts := &mockOAuthAccountRepository{
				FindByProviderFunc: func(ctx context.Context, provider, providerUID string) (*auth.OAuthAccount, error) {
					if tt.existingAccount != nil {
						return tt.existingAccount, nil
					}
					return nil, auth.ErrUserNotFound
				},
				CreateFunc: func(ctx context.Context, account *auth.OAuthAccount) error {
					linkCalled = true
					return nil
				},
			}
			creator := &mockOAuthUserCreator{
				CreateUserWithOAuthAccountFunc: func(ctx context.Context, user *auth.User, account *auth.OAuthAccount) error {
					creatorCalled = true
					if tt.creatorErr != nil {
						return tt.creatorErr
					}
					user.ID = 7
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
					return &auth.OAuthUserInfo{ProviderUID: "google-uid-1", Email: tt.providerEmail}, nil
				},
			}

			uc := auth.NewOAuthUsecase(
				users, oauthAccts, creator, stateStore,
				&mockJWTGenerator{},
				map[string]auth.OAuthProvider{providerName: provider},
			)

			token, err := uc.HandleCallback(context.Background(), providerName, "code", "state")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("HandleCallback error = %v, want %v", err, tt.wantErr)
				}
				if token != "" {
					t.Errorf("token = %q, want empty on error", token)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if token == "" {
					t.Error("token is empty")
				}
			}
			if linkCalled {
				t.Error("OAuthAccount auto-link (Create) must never be called")
			}
			if creatorCalled != tt.wantCreatorCalled {
				t.Errorf("creator called = %v, want %v", creatorCalled, tt.wantCreatorCalled)
			}
			if tt.wantLookupEmail != "" && lookupEmail != tt.wantLookupEmail {
				t.Errorf("FindByEmail called with %q, want %q", lookupEmail, tt.wantLookupEmail)
			}
		})
	}
}
