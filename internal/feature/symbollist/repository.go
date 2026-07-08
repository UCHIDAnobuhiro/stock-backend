package symbollist

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/sqlc"
)

// repository は Repository / LogoSymbolRepository の sqlc ベース実装です。
type repository struct {
	db *sql.DB
	q  *symbollistsqlc.Queries
}

var (
	_ Repository           = (*repository)(nil)
	_ LogoSymbolRepository = (*repository)(nil)
)

// NewRepository は指定された *sql.DB で repository の新しいインスタンスを生成します。
func NewRepository(db *sql.DB) *repository {
	return &repository{db: db, q: symbollistsqlc.New(db)}
}

// ListActive はコード昇順にすべてのアクティブな銘柄を返します。
func (r *repository) ListActive(ctx context.Context) ([]Symbol, error) {
	rows, err := r.q.ListActiveSymbols(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Symbol, 0, len(rows))
	for _, row := range rows {
		out = append(out, symbolFromSQLC(row))
	}
	return out, nil
}

// Exists は指定されたコードの銘柄が存在するかを返します。
func (r *repository) Exists(ctx context.Context, code string) (bool, error) {
	return r.q.SymbolExists(ctx, code)
}

// UpdateLogoURL は指定された銘柄のロゴURLと取得日時を更新します。
// 対象行が存在しない場合はエラーとせず警告ログを出力します（バッチの続行を優先するため）。
func (r *repository) UpdateLogoURL(ctx context.Context, code, logoURL string, updatedAt time.Time) error {
	rowsAffected, err := r.q.UpdateSymbolLogoURL(ctx, symbollistsqlc.UpdateSymbolLogoURLParams{
		Code:          code,
		LogoUrl:       sql.NullString{String: logoURL, Valid: true},
		LogoUpdatedAt: sql.NullTime{Time: updatedAt, Valid: true},
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		slog.Warn("UpdateLogoURL: no matching symbol found", "code", code)
	}
	return nil
}

// symbolFromSQLC は sqlc 生成モデルをドメインエンティティに変換します。
func symbolFromSQLC(m symbollistsqlc.Symbol) Symbol {
	var logoURL *string
	if m.LogoUrl.Valid {
		s := m.LogoUrl.String
		logoURL = &s
	}
	var logoUpdatedAt *time.Time
	if m.LogoUpdatedAt.Valid {
		t := m.LogoUpdatedAt.Time
		logoUpdatedAt = &t
	}
	return Symbol{
		Code:          m.Code,
		Name:          m.Name,
		Market:        m.Market,
		Timezone:      m.Timezone,
		LogoURL:       logoURL,
		LogoUpdatedAt: logoUpdatedAt,
		IsActive:      m.IsActive,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}
