package watchlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/sqlc"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// repository は Repository の sqlc ベース実装です。
type repository struct {
	db *sql.DB
	q  *watchlistsqlc.Queries
}

var _ Repository = (*repository)(nil)

// NewRepository は指定された *sql.DB で repository の新しいインスタンスを生成します。
func NewRepository(db *sql.DB) *repository {
	return &repository{db: db, q: watchlistsqlc.New(db)}
}

// ListByUser はユーザーのウォッチリストを sort_key 昇順で返します。
func (r *repository) ListByUser(ctx context.Context, userID int64) ([]UserSymbol, error) {
	rows, err := r.q.ListWatchlistByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]UserSymbol, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserSymbol{
			ID:         row.ID,
			UserID:     row.UserID,
			SymbolCode: row.SymbolCode,
			SortKey:    int(row.SortKey),
			CreatedAt:  row.CreatedAt,
			UpdatedAt:  row.UpdatedAt,
		})
	}
	return out, nil
}

// Add はウォッチリストに銘柄を追加します。
// 重複エントリは ErrAlreadyInWatchlist、FK 違反は ErrSymbolNotFound を返します。
func (r *repository) Add(ctx context.Context, entry UserSymbol) error {
	err := r.q.InsertWatchlist(ctx, watchlistsqlc.InsertWatchlistParams{
		UserID:     entry.UserID,
		SymbolCode: entry.SymbolCode,
		SortKey:    int64(entry.SortKey),
	})
	return mapWatchlistPGErr(err)
}

// Remove はウォッチリストから銘柄を削除します。
// 対象が存在しない場合は ErrNotInWatchlist を返します。
func (r *repository) Remove(ctx context.Context, userID int64, symbolCode string) error {
	rowsAffected, err := r.q.DeleteWatchlist(ctx, watchlistsqlc.DeleteWatchlistParams{
		UserID:     userID,
		SymbolCode: symbolCode,
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotInWatchlist
	}
	return nil
}

// UpdateSortKeys はウォッチリストの sort_key をトランザクション内で一括更新します。
// 冒頭でユーザーの全レコードを ORDER BY id で取得順にロックし、件数が entries と
// 一致しなければ ErrReorderCodesMismatch を返します（ListByUser との TOCTOU 競合対策。
// 取得順ロックにより並行 Reorder 同士のデッドロックも防ぎます）。
// (user_id, sort_key) のユニーク制約が一時的に違反しないよう、まず全レコードを
// 負値（-(i+1)）にシフトしてから最終値に更新します。各更新の更新行数が1でない場合
// （並行削除等で対象行が消えている場合）も ErrReorderCodesMismatch を返します。
func (r *repository) UpdateSortKeys(ctx context.Context, userID int64, entries []UserSymbol) error {
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

	// ユーザーの全レコードをロックし、entries と件数が一致するか検証する。
	// これにより ListByUser（呼び出し元）から本トランザクション開始までの間の
	// 並行変更を検出する。
	locked, err := qtx.LockWatchlistByUser(ctx, userID)
	if err != nil {
		return err
	}
	if len(locked) != len(entries) {
		return ErrReorderCodesMismatch
	}

	// Phase 1: 負値にシフト
	for i, e := range entries {
		rowsAffected, err := qtx.UpdateWatchlistSortKey(ctx, watchlistsqlc.UpdateWatchlistSortKeyParams{
			UserID:     userID,
			SymbolCode: e.SymbolCode,
			SortKey:    int64(-(i + 1)),
		})
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return ErrReorderCodesMismatch
		}
	}
	// Phase 2: 最終値に更新
	for _, e := range entries {
		rowsAffected, err := qtx.UpdateWatchlistSortKey(ctx, watchlistsqlc.UpdateWatchlistSortKeyParams{
			UserID:     userID,
			SymbolCode: e.SymbolCode,
			SortKey:    int64(e.SortKey),
		})
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return ErrReorderCodesMismatch
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

// AddWithNextSortKey は sort_key をトランザクション内で MAX+1 採番して銘柄を追加します。
// MAX(sort_key) 取得と INSERT を同一トランザクションで実行し、(user_id, sort_key) ユニーク制約で
// 並行追加の二重登録を最終的にブロックします。
func (r *repository) AddWithNextSortKey(ctx context.Context, userID int64, symbolCode string) error {
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

	maxKey, err := qtx.MaxWatchlistSortKey(ctx, userID)
	if err != nil {
		return err
	}
	if err := qtx.InsertWatchlist(ctx, watchlistsqlc.InsertWatchlistParams{
		UserID:     userID,
		SymbolCode: symbolCode,
		SortKey:    maxKey + 1,
	}); err != nil {
		return mapWatchlistPGErr(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

// mapWatchlistPGErr は PostgreSQL 制約違反をドメインエラーに変換します。
func mapWatchlistPGErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return ErrAlreadyInWatchlist
		case pgForeignKeyViolation:
			return ErrSymbolNotFound
		}
	}
	return err
}
