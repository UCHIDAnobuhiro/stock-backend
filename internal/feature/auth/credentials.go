package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

// User はシステムに登録されたユーザーを表します。
// 認証情報とユーザー管理用のメタデータを含みます。
type User struct {
	// ID はユーザーの一意な識別子です。
	ID int64

	// Email は認証に使用されるユーザーのメールアドレスです。
	// 全ユーザー間で一意である必要があります。
	Email string

	// PasswordHash はユーザーのハッシュ化されたパスワードです。
	// 平文パスワードを保存してはなりません。
	// OAuth専用ユーザーはパスワードを持たないため nil になります。
	PasswordHash *string

	// CreatedAt はユーザーが作成された日時です。
	CreatedAt time.Time

	// UpdatedAt はユーザーが最後に更新された日時です。
	UpdatedAt time.Time
}

// UserCreatedHook はユーザー新規作成後に呼び出されるフックのインターフェースです。
// usecase層でインターフェースを定義することで、transport層への依存を避けます。
type UserCreatedHook interface {
	OnUserCreated(ctx context.Context, userID int64) error
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

// usecase は認証ビジネスロジックを実装します。
type usecase struct {
	users     UserRepository
	sessions  SessionManager
	pepper    string
	dummyHash string // タイミング攻撃防止用のダミーハッシュ
}

// NewUsecase はusecaseの新しいインスタンスを生成します。
func NewUsecase(users UserRepository, sessions SessionManager, pepper string) *usecase {
	uc := &usecase{
		users:    users,
		sessions: sessions,
		pepper:   pepper,
	}
	// ペッパー適用済みのダミーハッシュを事前計算（タイミング攻撃防止用）
	pepperedDummy := uc.pepperPassword("dummy")
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte(pepperedDummy), bcrypt.DefaultCost)
	uc.dummyHash = string(dummyHash)
	return uc
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

// Login はユーザーを認証し、成功時にアクセストークンとリフレッシュトークンを返します。
// メールアドレスとパスワードを検証し、サーバー管理セッションを生成します。
// タイミング攻撃を防止するため、ユーザーが存在しない場合でもbcrypt比較を実行します。
func (u *usecase) Login(ctx context.Context, email, password string) (TokenPair, error) {
	// 過大なパスワードによる CPU 枯渇を防止するため、HMAC 計算前に上限を超えるものを弾く。
	// 上限超過のパスワードは正規パスワードと一致し得ないため汎用エラーを返す。
	// ユーザー存在有無に関わらず同じ経路で早期 return するため、ユーザー列挙にはつながらない。
	if len(password) > maxPasswordLength {
		return TokenPair{}, ErrInvalidCredentials
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
		return TokenPair{}, ErrInvalidCredentials
	}

	// JWTとサーバー管理リフレッシュセッションを一体として発行する。
	pair, sessionErr := u.sessions.Issue(ctx, user.ID, user.Email)
	if sessionErr != nil {
		return TokenPair{}, fmt.Errorf("failed to issue session: %w", sessionErr)
	}
	return pair, nil
}

// Refresh はリフレッシュトークンを新しいトークンペアへ交換します。
func (u *usecase) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	return u.sessions.Refresh(ctx, refreshToken)
}

// Logout はリフレッシュトークンが属するセッション系列を失効させます。
func (u *usecase) Logout(ctx context.Context, refreshToken string) error {
	return u.sessions.Revoke(ctx, refreshToken)
}

// NormalizeEmail はメールアドレスを保存・検索の前段で正規化します。
// 前後の空白を除去し、すべて小文字化することで、
// `User@Example.com ` と `user@example.com` を同一のメールとして扱います。
// これにより重複アカウントの作成や OAuth 自動リンクの不一致を防ぎます。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
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
