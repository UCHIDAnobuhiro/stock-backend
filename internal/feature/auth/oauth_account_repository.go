package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/sqlc"
)

// oauthAccountRepository は OAuthAccountRepository の sqlc ベース実装です。
type oauthAccountRepository struct {
	q *authsqlc.Queries
}

var _ OAuthAccountRepository = (*oauthAccountRepository)(nil)

// NewOAuthAccountRepository は指定された *sql.DB で oauthAccountRepository の新しいインスタンスを生成します。
func NewOAuthAccountRepository(db *sql.DB) *oauthAccountRepository {
	return &oauthAccountRepository{q: authsqlc.New(db)}
}

// FindByProvider はプロバイダー名とプロバイダー UID で OAuthAccount を検索します。
// 存在しない場合は ErrUserNotFound を返します。
func (r *oauthAccountRepository) FindByProvider(ctx context.Context, provider, providerUID string) (*OAuthAccount, error) {
	row, err := r.q.FindOAuthAccountByProvider(ctx, authsqlc.FindOAuthAccountByProviderParams{
		Provider:    provider,
		ProviderUid: providerUID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	acct := oauthAccountFromSQLC(row)
	return &acct, nil
}
