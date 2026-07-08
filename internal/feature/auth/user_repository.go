package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/sqlc"
)

// pgErrUniqueViolation は PostgreSQL のユニーク制約違反コードです。
const pgErrUniqueViolation = "23505"

// userRepository は UserRepository / OAuthUserCreator の sqlc ベース実装です。
type userRepository struct {
	db *sql.DB
	q  *authsqlc.Queries
}

var (
	_ UserRepository   = (*userRepository)(nil)
	_ OAuthUserCreator = (*userRepository)(nil)
)

// NewUserRepository は指定された *sql.DB で userRepository の新しいインスタンスを生成します。
func NewUserRepository(db *sql.DB) *userRepository {
	return &userRepository{db: db, q: authsqlc.New(db)}
}

// Create はユーザーをデータベースに追加します。
// 同じメールアドレスのユーザーが既に存在する場合、ErrEmailAlreadyExists を返します。
func (r *userRepository) Create(ctx context.Context, u *User) error {
	if u == nil {
		return errors.New("user is nil")
	}
	row, err := r.q.CreateUser(ctx, authsqlc.CreateUserParams{
		Email:        u.Email,
		PasswordHash: toNullString(u.PasswordHash),
	})
	if err != nil {
		return mapEmailUniqueErr(err)
	}
	*u = userFromSQLC(row)
	return nil
}

// FindByEmail はメールアドレスでユーザーを取得します。
// ユーザーが存在しない場合、ErrUserNotFound を返します。
func (r *userRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	row, err := r.q.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	u := userFromSQLC(row)
	return &u, nil
}

// FindByID は ID でユーザーを取得します。
// ユーザーが存在しない場合、ErrUserNotFound を返します。
func (r *userRepository) FindByID(ctx context.Context, id int64) (*User, error) {
	row, err := r.q.FindUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	u := userFromSQLC(row)
	return &u, nil
}

// CreateUserWithOAuthAccount は User と OAuthAccount をトランザクション内で原子的に作成します。
func (r *userRepository) CreateUserWithOAuthAccount(ctx context.Context, user *User, account *OAuthAccount) error {
	if user == nil || account == nil {
		return errors.New("user or account is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	qtx := r.q.WithTx(tx)
	userRow, err := qtx.CreateUser(ctx, authsqlc.CreateUserParams{
		Email:        user.Email,
		PasswordHash: toNullString(user.PasswordHash),
	})
	if err != nil {
		return mapEmailUniqueErr(err)
	}
	*user = userFromSQLC(userRow)

	account.UserID = user.ID
	acctRow, err := qtx.CreateOAuthAccount(ctx, authsqlc.CreateOAuthAccountParams{
		UserID:      account.UserID,
		Provider:    account.Provider,
		ProviderUid: account.ProviderUID,
	})
	if err != nil {
		return err
	}
	*account = oauthAccountFromSQLC(acctRow)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

// userFromSQLC は sqlc 生成モデルをドメインエンティティに変換します。
func userFromSQLC(m authsqlc.User) User {
	var pwd *string
	if m.PasswordHash.Valid {
		s := m.PasswordHash.String
		pwd = &s
	}
	return User{
		ID:           m.ID,
		Email:        m.Email,
		PasswordHash: pwd,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

// oauthAccountFromSQLC は sqlc 生成モデルをドメインエンティティに変換します。
func oauthAccountFromSQLC(m authsqlc.OauthAccount) OAuthAccount {
	return OAuthAccount{
		UserID:      m.UserID,
		Provider:    m.Provider,
		ProviderUID: m.ProviderUid,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// toNullString は *string を sql.NullString に変換します。nil の場合は Valid=false です。
func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// mapEmailUniqueErr は PostgreSQL のユニーク制約違反を ErrEmailAlreadyExists にマッピングします。
func mapEmailUniqueErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
		return ErrEmailAlreadyExists
	}
	return err
}
