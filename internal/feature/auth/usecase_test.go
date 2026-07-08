package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
)

const testPepper = "test-pepper-secret-32chars-long!"

// pepperPasswordForTest はテスト用にHMAC-SHA256でパスワードにペッパーを適用します。
func pepperPasswordForTest(password, pepper string) string {
	if pepper == "" {
		return password
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))
}

// mockUserRepository はUserRepositoryインターフェースのモック実装です。
// テスト中のデータベース操作をシミュレートします。
type mockUserRepository struct {
	// CreateFunc はCreateメソッド呼び出し時に実行されます。
	CreateFunc func(ctx context.Context, user *auth.User) error
	// FindByEmailFunc はFindByEmailメソッド呼び出し時に実行されます。
	FindByEmailFunc func(ctx context.Context, email string) (*auth.User, error)
	// FindByIDFunc はFindByIDメソッド呼び出し時に実行されます。
	FindByIDFunc func(ctx context.Context, id int64) (*auth.User, error)
}

// mockJWTGenerator はJWTGeneratorインターフェースのモック実装です。
// テスト中のJWTトークン生成をシミュレートします。
type mockJWTGenerator struct {
	// GenerateTokenFunc はGenerateTokenメソッド呼び出し時に実行されます。
	GenerateTokenFunc func(userID int64, email string) (string, error)
}

// GenerateToken はGenerateTokenメソッドのモック実装です。
func (m *mockJWTGenerator) GenerateToken(userID int64, email string) (string, error) {
	if m.GenerateTokenFunc != nil {
		return m.GenerateTokenFunc(userID, email)
	}
	// デフォルト: ダミートークンを返す
	return "mock-jwt-token", nil
}

// Create はCreateメソッドのモック実装です。
func (m *mockUserRepository) Create(ctx context.Context, user *auth.User) error {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, user)
	}
	return nil // デフォルト: 成功
}

// FindByEmail はFindByEmailメソッドのモック実装です。
func (m *mockUserRepository) FindByEmail(ctx context.Context, email string) (*auth.User, error) {
	if m.FindByEmailFunc != nil {
		return m.FindByEmailFunc(ctx, email)
	}
	// デフォルト: ユーザー未検出エラーを返す
	return nil, errors.New("user not found")
}

// FindByID はFindByIDメソッドのモック実装です。
func (m *mockUserRepository) FindByID(ctx context.Context, id int64) (*auth.User, error) {
	if m.FindByIDFunc != nil {
		return m.FindByIDFunc(ctx, id)
	}
	// デフォルト: ユーザー未検出エラーを返す
	return nil, errors.New("user not found")
}

// createTestUser はテスト用にハッシュ化パスワードを持つテストユーザーを作成します。
// このヘルパーはコードの重複を削減し、テストの保守性を向上させます。
func createTestUser(t *testing.T, id int64, email, password string) *auth.User {
	t.Helper()
	pepperedPassword := pepperPasswordForTest(password, testPepper)
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(pepperedPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	hashedStr := string(hashedPassword)
	return &auth.User{
		ID:           id,
		Email:        email,
		PasswordHash: &hashedStr,
	}
}

// assertError はエラーが期待値と一致するかチェックします。
// このヘルパーは全テストのエラーアサーションを標準化します。
func assertError(t *testing.T, err error, wantErr bool, errMsg string) {
	t.Helper()
	if wantErr {
		if err == nil {
			t.Fatal("expected error but got nil")
		}
		if errMsg != "" && err.Error() != errMsg {
			t.Errorf("expected error '%s', got: '%s'", errMsg, err.Error())
		}
	} else {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

// verifyBcryptHash はハッシュ化パスワードが平文パスワードと一致するかチェックします。
// このヘルパーはbcrypt検証ロジックをカプセル化します。
func verifyBcryptHash(t *testing.T, hashedPassword, plainPassword string) {
	t.Helper()
	if len(hashedPassword) == 0 || hashedPassword == plainPassword {
		t.Error("password is not hashed")
	}
	pepperedPassword := pepperPasswordForTest(plainPassword, testPepper)
	if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(pepperedPassword)); err != nil {
		t.Errorf("invalid bcrypt hash: %v", err)
	}
}

// TestAuthUsecase_Signup はサインアップのビジネスロジック（パスワード検証、ハッシュ化、リポジトリ呼び出し）をテストします。
func TestAuthUsecase_Signup(t *testing.T) {
	t.Parallel() // テスト関数の並列実行を有効化

	tests := []struct {
		name             string
		email            string
		password         string
		wantErr          bool
		errMsg           string
		verifyBcryptHash bool
		repositoryErr    error
	}{
		{
			name:             "successful signup",
			email:            "test@example.com",
			password:         "password12345",
			wantErr:          false,
			verifyBcryptHash: true,
		},
		{
			name:     "password too short",
			email:    "test@example.com",
			password: "short",
			wantErr:  true,
			errMsg:   "password must be at least 12 characters long",
		},
		{
			name:             "password at minimum length",
			email:            "test@example.com",
			password:         "123456789012",
			wantErr:          false,
			verifyBcryptHash: true,
		},
		{
			name:     "empty password",
			email:    "test@example.com",
			password: "",
			wantErr:  true,
			errMsg:   "password must be at least 12 characters long",
		},
		{
			name:             "long password",
			email:            "test@example.com",
			password:         "this-is-a-very-long-password-with-many-characters",
			wantErr:          false,
			verifyBcryptHash: true,
		},
		{
			name:             "password at maximum length",
			email:            "test@example.com",
			password:         strings.Repeat("a", 1024),
			wantErr:          false,
			verifyBcryptHash: true,
		},
		{
			name:     "password too long",
			email:    "test@example.com",
			password: strings.Repeat("a", 1025),
			wantErr:  true,
			errMsg:   "password must be at most 1024 characters long",
		},
		{
			name:          "repository create failure",
			email:         "test@example.com",
			password:      "password12345",
			wantErr:       true,
			repositoryErr: errors.New("database error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // サブテストの並列実行を有効化

			mockRepo := &mockUserRepository{
				CreateFunc: func(ctx context.Context, user *auth.User) error {
					if tt.verifyBcryptHash {
						verifyBcryptHash(t, *user.PasswordHash, tt.password)
					}
					if tt.repositoryErr != nil {
						return tt.repositoryErr
					}
					return nil
				},
			}
			mockJWT := &mockJWTGenerator{}

			uc := auth.NewUsecase(mockRepo, mockJWT, testPepper)
			_, err := uc.Signup(context.Background(), tt.email, tt.password)

			// Assert error expectations
			assertError(t, err, tt.wantErr, tt.errMsg)
			if tt.repositoryErr != nil && !errors.Is(err, tt.repositoryErr) {
				t.Errorf("expected error '%v', got: %v", tt.repositoryErr, err)
			}
		})
	}
}

func TestAuthUsecase_Login(t *testing.T) {
	t.Parallel() // enable parallel execution for test function

	// Create test user using helper function
	testUser := createTestUser(t, 1, "test@example.com", "password12345")

	tests := []struct {
		name              string
		email             string
		password          string
		wantErr           bool
		errMsg            string
		expectedToken     string
		findByEmailResult *auth.User
		findByEmailErr    error
		jwtGenerateErr    error
		verifyJWTParams   bool
	}{
		{
			name:              "successful login",
			email:             "test@example.com",
			password:          "password12345",
			wantErr:           false,
			expectedToken:     "mock-jwt-token",
			findByEmailResult: testUser,
			verifyJWTParams:   true,
		},
		{
			name:           "user not found",
			email:          "wrong@example.com",
			password:       "password12345",
			wantErr:        true,
			errMsg:         "invalid email or password",
			findByEmailErr: errors.New("user not found"),
		},
		{
			name:              "incorrect password",
			email:             "test@example.com",
			password:          "wrong-password",
			wantErr:           true,
			errMsg:            "invalid email or password",
			findByEmailResult: testUser,
		},
		{
			name:              "JWT generation failure",
			email:             "test@example.com",
			password:          "password12345",
			wantErr:           true,
			errMsg:            "failed to generate token: failed to sign token",
			findByEmailResult: testUser,
			jwtGenerateErr:    errors.New("failed to sign token"),
		},
		{
			name:              "edge case: empty password with valid user",
			email:             "test@example.com",
			password:          "",
			wantErr:           true,
			errMsg:            "invalid email or password",
			findByEmailResult: testUser,
		},
		{
			name:              "password too long is rejected before hashing",
			email:             "test@example.com",
			password:          strings.Repeat("a", 1025),
			wantErr:           true,
			errMsg:            "invalid email or password",
			findByEmailResult: testUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // サブテストの並列実行を有効化

			mockRepo := &mockUserRepository{
				FindByEmailFunc: func(ctx context.Context, email string) (*auth.User, error) {
					if tt.findByEmailErr != nil {
						return nil, tt.findByEmailErr
					}
					return tt.findByEmailResult, nil
				},
			}
			mockJWT := &mockJWTGenerator{
				GenerateTokenFunc: func(userID int64, email string) (string, error) {
					if tt.verifyJWTParams {
						if userID != testUser.ID || email != testUser.Email {
							t.Errorf("unexpected userID or email: got userID=%d, email=%s", userID, email)
						}
					}
					if tt.jwtGenerateErr != nil {
						return "", tt.jwtGenerateErr
					}
					return tt.expectedToken, nil
				},
			}

			uc := auth.NewUsecase(mockRepo, mockJWT, testPepper)
			token, err := uc.Login(context.Background(), tt.email, tt.password)

			// エラーの期待値を検証
			assertError(t, err, tt.wantErr, tt.errMsg)

			// 成功ケースの期待値を検証
			if !tt.wantErr {
				if token == "" {
					t.Error("token is empty")
				}
				if tt.expectedToken != "" && token != tt.expectedToken {
					t.Errorf("expected token '%s', got: '%s'", tt.expectedToken, token)
				}
			}
		})
	}
}

// TestAuthUsecase_Signup_NormalizesEmail はサインアップ時にメールアドレスが
// 小文字化・trim されてリポジトリに保存されることを検証します（重複アカウント防止）。
func TestAuthUsecase_Signup_NormalizesEmail(t *testing.T) {
	t.Parallel()

	var storedEmail string
	mockRepo := &mockUserRepository{
		CreateFunc: func(ctx context.Context, user *auth.User) error {
			storedEmail = user.Email
			return nil
		},
	}
	uc := auth.NewUsecase(mockRepo, &mockJWTGenerator{}, testPepper)

	if _, err := uc.Signup(context.Background(), "  User@Example.COM ", "password12345"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if storedEmail != "user@example.com" {
		t.Errorf("stored email = %q, want %q", storedEmail, "user@example.com")
	}
}

// TestAuthUsecase_Login_NormalizesEmail はログイン時に正規化済みメールアドレスで
// ユーザー検索が行われることを検証します（レート制限キーとの不整合を防ぐ）。
func TestAuthUsecase_Login_NormalizesEmail(t *testing.T) {
	t.Parallel()

	testUser := createTestUser(t, 1, "user@example.com", "password12345")

	var lookupEmail string
	mockRepo := &mockUserRepository{
		FindByEmailFunc: func(ctx context.Context, email string) (*auth.User, error) {
			lookupEmail = email
			return testUser, nil
		},
	}
	uc := auth.NewUsecase(mockRepo, &mockJWTGenerator{}, testPepper)

	token, err := uc.Login(context.Background(), "  User@Example.COM ", "password12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("token is empty")
	}
	if lookupEmail != "user@example.com" {
		t.Errorf("FindByEmail called with %q, want %q", lookupEmail, "user@example.com")
	}
}

// TestAuthUsecase_PepperApplied はペッパーが正しくパスワードに適用されることを検証します。
func TestAuthUsecase_PepperApplied(t *testing.T) {
	t.Parallel()

	t.Run("signup hash does not match raw password", func(t *testing.T) {
		t.Parallel()

		mockRepo := &mockUserRepository{
			CreateFunc: func(ctx context.Context, user *auth.User) error {
				// 生パスワードではハッシュが一致しないことを確認
				err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte("password12345"))
				if err == nil {
					t.Error("hash should not match raw password when pepper is applied")
				}
				// ペッパー適用済みパスワードでは一致することを確認
				peppered := pepperPasswordForTest("password12345", testPepper)
				if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(peppered)); err != nil {
					t.Errorf("hash should match peppered password: %v", err)
				}
				return nil
			},
		}
		mockJWT := &mockJWTGenerator{}

		uc := auth.NewUsecase(mockRepo, mockJWT, testPepper)
		_, err := uc.Signup(context.Background(), "test@example.com", "password12345")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("long password is not truncated by bcrypt", func(t *testing.T) {
		t.Parallel()

		// 72バイト超のパスワード（bcryptの入力制限を超える）
		longPassword := "this-is-a-very-long-password-that-exceeds-72-bytes-limit-of-bcrypt-algorithm-and-should-still-work"
		var storedHash string

		mockRepo := &mockUserRepository{
			CreateFunc: func(ctx context.Context, user *auth.User) error {
				storedHash = *user.PasswordHash
				return nil
			},
		}
		mockJWT := &mockJWTGenerator{}

		uc := auth.NewUsecase(mockRepo, mockJWT, testPepper)
		_, err := uc.Signup(context.Background(), "test@example.com", longPassword)
		if err != nil {
			t.Fatalf("unexpected signup error: %v", err)
		}

		// HMAC-SHA256により64文字固定のhex出力になるため、bcryptの72バイト制限に収まる
		peppered := pepperPasswordForTest(longPassword, testPepper)
		if len(peppered) != 64 {
			t.Errorf("expected peppered password length 64, got %d", len(peppered))
		}
		if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(peppered)); err != nil {
			t.Errorf("hash should match peppered long password: %v", err)
		}
	})
}
