package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// minPasswordLength はパスワードの最低文字数を定義します。
	// NIST SP 800-63B はユーザー選択パスワードに 8 文字以上を要求しているが、
	// 辞書攻撃への耐性を高めるため、より長い 12 文字を最低長とする。
	minPasswordLength = 12

	// maxPasswordLength はパスワードの最大バイト数を定義します。
	// パスワードは HMAC-SHA256 ペッパー適用を経て bcrypt にかけられるため、
	// 上限がないと過大なパスワード本体に対する HMAC 計算で CPU を浪費させられる。
	// これを防ぐため上限を設ける（len() はバイト数を返す）。
	maxPasswordLength = 1024

	// EnvKeyPasswordPepper はパスワードペッパーの環境変数キーです。
	EnvKeyPasswordPepper = "PASSWORD_PEPPER"
)

// UserCreatedHook はユーザー新規作成後に呼び出されるフックのインターフェースです。
// usecase層でインターフェースを定義することで、transport層への依存を避けます。
type UserCreatedHook interface {
	OnUserCreated(ctx context.Context, userID int64) error
}

// OAuthUserInfo はOAuth2プロバイダーから取得したユーザー情報です。
type OAuthUserInfo struct {
	ProviderUID string // プロバイダー側のユーザー一意ID
	Email       string // 検証済みメールアドレス
}

// OAuthProvider はOAuth2プロバイダーの抽象化インターフェースです。
// インターフェースはコンシューマー（usecase）が定義します。
type OAuthProvider interface {
	// AuthorizationURL はPKCEのcodeChallenge付きの認可URLを生成します。
	AuthorizationURL(state, codeChallenge string) string
	// ExchangeCode はauthorization codeをユーザー情報に交換します。
	ExchangeCode(ctx context.Context, code, codeVerifier string) (*OAuthUserInfo, error)
}

// OAuthStateStore はPKCE stateの一時保存を抽象化します。
type OAuthStateStore interface {
	// SaveState はstateとcodeVerifierをTTL付きで保存します。
	SaveState(ctx context.Context, state, codeVerifier string, ttl time.Duration) error
	// ConsumeState はstateを検索して削除し、codeVerifierを返します。
	// stateが存在しない・期限切れの場合はErrStateNotFoundを返します。
	ConsumeState(ctx context.Context, state string) (codeVerifier string, err error)
}

// OAuthAccountRepository はoauth_accountsテーブルの永続化を抽象化します。
type OAuthAccountRepository interface {
	// FindByProvider はプロバイダー名とプロバイダーUIDでOAuthAccountを検索します。
	FindByProvider(ctx context.Context, provider, providerUID string) (*OAuthAccount, error)
	// Create はOAuthAccountを新規作成します。
	Create(ctx context.Context, account *OAuthAccount) error
}

// OAuthUserCreator はOAuth新規ユーザー作成時にUserとOAuthAccountを
// トランザクション内で原子的に作成します。
// 実装はadapters層がDB固有のトランザクション処理を担います。
type OAuthUserCreator interface {
	CreateUserWithOAuthAccount(ctx context.Context, user *User, account *OAuthAccount) error
}

// UserRepository はユーザーエンティティの永続化層を抽象化します。
// Goの慣例に従い、インターフェースはプロバイダー（adapters）ではなくコンシューマー（usecase）が定義します。
type UserRepository interface {
	// Create は新しいユーザーをストレージに永続化します。
	// 同じメールアドレスのユーザーが既に存在する場合、エラーを返します。
	Create(ctx context.Context, user *User) error

	// FindByEmail は指定されたメールアドレスに一致するユーザーを取得します。
	// ユーザーが存在しない場合、エラーを返します。
	FindByEmail(ctx context.Context, email string) (*User, error)

	// FindByID は指定されたIDに一致するユーザーを取得します。
	// ユーザーが存在しない場合、エラーを返します。
	FindByID(ctx context.Context, id int64) (*User, error)
}

// JWTGenerator はJWTトークン生成のインターフェースを定義します。
// Goの慣例に従い、インターフェースはプロバイダー（platform/jwt）ではなくコンシューマー（usecase）が定義します。
type JWTGenerator interface {
	// GenerateToken は指定されたユーザーの署名済みJWTトークンを生成します。
	GenerateToken(userID int64, email string) (string, error)
}

// usecase は認証ビジネスロジックを実装します。
type usecase struct {
	users        UserRepository
	jwtGenerator JWTGenerator
	pepper       string
	dummyHash    string // タイミング攻撃防止用のダミーハッシュ
}

// NewUsecase はusecaseの新しいインスタンスを生成します。
func NewUsecase(users UserRepository, jwtGenerator JWTGenerator, pepper string) *usecase {
	uc := &usecase{
		users:        users,
		jwtGenerator: jwtGenerator,
		pepper:       pepper,
	}
	// ペッパー適用済みのダミーハッシュを事前計算（タイミング攻撃防止用）
	pepperedDummy := uc.pepperPassword("dummy")
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte(pepperedDummy), bcrypt.DefaultCost)
	uc.dummyHash = string(dummyHash)
	return uc
}

// pepperPassword はHMAC-SHA256を使用してパスワードにペッパーを適用します。
// bcryptの72バイト制限を回避するため、HMAC-SHA256で固定長のハッシュを生成します。
func (u *usecase) pepperPassword(password string) string {
	if u.pepper == "" {
		return password
	}
	mac := hmac.New(sha256.New, []byte(u.pepper))
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))
}

// Signup はハッシュ化されたパスワードで新規ユーザーを登録します。
// 成功時に作成されたユーザーのIDを返します。
func (u *usecase) Signup(ctx context.Context, email, password string) (int64, error) {
	// メールアドレスを正規化（小文字化・trim）してから保存する。
	email = NormalizeEmail(email)

	// パスワード強度を検証
	if err := validatePassword(password); err != nil {
		return 0, err
	}

	pepperedPassword := u.pepperPassword(password)
	hashed, err := bcrypt.GenerateFromPassword([]byte(pepperedPassword), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("failed to hash password: %w", err)
	}
	hashedStr := string(hashed)
	user := &User{Email: email, PasswordHash: &hashedStr}
	if err := u.users.Create(ctx, user); err != nil {
		return 0, err
	}
	return user.ID, nil
}

// Login はユーザーを認証し、成功時にJWTトークンを返します。
// メールアドレスとパスワードを検証し、署名済みJWTトークンを生成します。
// タイミング攻撃を防止するため、ユーザーが存在しない場合でもbcrypt比較を実行します。
func (u *usecase) Login(ctx context.Context, email, password string) (string, error) {
	// 過大なパスワードによる CPU 枯渇を防止するため、HMAC 計算前に上限を超えるものを弾く。
	// 上限超過のパスワードは正規パスワードと一致し得ないため汎用エラーを返す。
	// ユーザー存在有無に関わらず同じ経路で早期 return するため、ユーザー列挙にはつながらない。
	if len(password) > maxPasswordLength {
		return "", ErrInvalidCredentials
	}

	// 保存時と同じ正規化を施してから検索する（ケース依存の不一致を防ぐ）。
	email = NormalizeEmail(email)

	// メールアドレスでユーザーを検索
	user, err := u.users.FindByEmail(ctx, email)

	// ユーザーが存在しない場合のタイミング攻撃緩和用ダミーハッシュ
	// bcrypt.CompareHashAndPasswordが常に呼ばれることを保証する
	passwordHash := u.dummyHash
	if err == nil && user.PasswordHash != nil {
		passwordHash = *user.PasswordHash
	}

	// タイミング攻撃防止のため、常にパスワードを検証
	// 第1引数はハッシュ化パスワード、第2引数は平文パスワード
	pepperedPassword := u.pepperPassword(password)
	compareErr := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(pepperedPassword))

	// ユーザー未検出またはパスワード不一致の場合、汎用エラーを返す
	if err != nil || compareErr != nil {
		return "", ErrInvalidCredentials
	}

	// 注入されたジェネレーターを使用してJWTトークンを生成
	token, tokenErr := u.jwtGenerator.GenerateToken(user.ID, user.Email)
	if tokenErr != nil {
		return "", fmt.Errorf("failed to generate token: %w", tokenErr)
	}

	return token, nil
}

// validatePassword はパスワードがセキュリティ要件を満たしているかチェックします。
func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters long", minPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return fmt.Errorf("password must be at most %d characters long", maxPasswordLength)
	}
	return nil
}
