package watchlist

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

// setupTestDB はテスト用 DB を作成し、watchlists の FK 先である users / symbols を
// あらかじめ投入します（FK 制約があるため必須）。
func setupTestDB(t *testing.T) (*sql.DB, userIDs) {
	t.Helper()
	db := dbtest.OpenIsolatedDB(t)

	ctx := context.Background()
	users := userIDs{}
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('u1@example.com', 'p') RETURNING id`).Scan(&users.u1))
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('u2@example.com', 'p') RETURNING id`).Scan(&users.u2))

	_, err := db.ExecContext(ctx,
		`INSERT INTO symbols (code, name, market, timezone) VALUES
		   ('AAPL', 'Apple', 'NASDAQ', 'America/New_York'),
		   ('GOOGL', 'Alphabet', 'NASDAQ', 'America/New_York'),
		   ('MSFT', 'Microsoft', 'NASDAQ', 'America/New_York')`)
	require.NoError(t, err)

	return db, users
}

type userIDs struct {
	u1, u2 int64
}

func TestWatchlistRepository_Add_and_ListByUser(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0,
	}))
	require.NoError(t, repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "GOOGL", SortKey: 1,
	}))

	list, err := repo.ListByUser(context.Background(), ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "AAPL", list[0].SymbolCode)
	assert.Equal(t, 0, list[0].SortKey)
	assert.Equal(t, "GOOGL", list[1].SymbolCode)
	assert.Equal(t, 1, list[1].SortKey)
}

func TestWatchlistRepository_Add_DuplicateEntry(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0,
	}))
	err := repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "AAPL", SortKey: 1,
	})
	assert.ErrorIs(t, err, ErrAlreadyInWatchlist)
}

func TestWatchlistRepository_Add_UnknownSymbol(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	err := repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "UNKNOWN", SortKey: 0,
	})
	assert.ErrorIs(t, err, ErrSymbolNotFound)
}

func TestWatchlistRepository_Remove(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.Add(context.Background(), UserSymbol{
		UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0,
	}))

	require.NoError(t, repo.Remove(context.Background(), ids.u1, "AAPL"))
	list, err := repo.ListByUser(context.Background(), ids.u1)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestWatchlistRepository_Remove_NotFound(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	err := repo.Remove(context.Background(), ids.u1, "AAPL")
	assert.ErrorIs(t, err, ErrNotInWatchlist)
}

func TestWatchlistRepository_AddWithNextSortKey(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.AddWithNextSortKey(context.Background(), ids.u1, "AAPL"))
	require.NoError(t, repo.AddWithNextSortKey(context.Background(), ids.u1, "GOOGL"))
	require.NoError(t, repo.AddWithNextSortKey(context.Background(), ids.u1, "MSFT"))

	list, err := repo.ListByUser(context.Background(), ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, 0, list[0].SortKey)
	assert.Equal(t, "AAPL", list[0].SymbolCode)
	assert.Equal(t, 1, list[1].SortKey)
	assert.Equal(t, "GOOGL", list[1].SymbolCode)
	assert.Equal(t, 2, list[2].SortKey)
	assert.Equal(t, "MSFT", list[2].SymbolCode)
}

func TestWatchlistRepository_AddWithNextSortKey_FirstEntry(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.AddWithNextSortKey(context.Background(), ids.u1, "AAPL"))

	list, err := repo.ListByUser(context.Background(), ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 0, list[0].SortKey)
}

func TestWatchlistRepository_AddWithNextSortKey_DuplicateSymbol(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)

	require.NoError(t, repo.AddWithNextSortKey(context.Background(), ids.u1, "AAPL"))
	err := repo.AddWithNextSortKey(context.Background(), ids.u1, "AAPL")
	assert.ErrorIs(t, err, ErrAlreadyInWatchlist)
}

func TestWatchlistRepository_UpdateSortKeys(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "GOOGL", SortKey: 1}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "MSFT", SortKey: 2}))

	// 並び替え: MSFT(0), AAPL(1), GOOGL(2)
	require.NoError(t, repo.UpdateSortKeys(ctx, ids.u1, []UserSymbol{
		{SymbolCode: "MSFT", SortKey: 0},
		{SymbolCode: "AAPL", SortKey: 1},
		{SymbolCode: "GOOGL", SortKey: 2},
	}))

	list, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "MSFT", list[0].SymbolCode)
	assert.Equal(t, "AAPL", list[1].SymbolCode)
	assert.Equal(t, "GOOGL", list[2].SymbolCode)
}

func TestWatchlistRepository_UpdateSortKeys_TargetRowMissing(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "GOOGL", SortKey: 1}))

	// entries の件数（2件）は DB の件数（2件）と一致するが、GOOGL の代わりに
	// 存在しない MSFT を指定することでロック件数チェックを素通りさせ、
	// UpdateWatchlistSortKey の更新行数0件チェックを踏ませる。
	err := repo.UpdateSortKeys(ctx, ids.u1, []UserSymbol{
		{SymbolCode: "AAPL", SortKey: 0},
		{SymbolCode: "MSFT", SortKey: 1},
	})
	assert.ErrorIs(t, err, ErrReorderCodesMismatch)

	// ロールバックされ、既存行の sort_key が変更されていないことを確認する。
	list, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "AAPL", list[0].SymbolCode)
	assert.Equal(t, 0, list[0].SortKey)
	assert.Equal(t, "GOOGL", list[1].SymbolCode)
	assert.Equal(t, 1, list[1].SortKey)
}

func TestWatchlistRepository_UpdateSortKeys_CountMismatch(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "GOOGL", SortKey: 1}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "MSFT", SortKey: 2}))

	// DB には3件あるが entries は2件のみ（並行削除等を想定した不一致ケース）。
	err := repo.UpdateSortKeys(ctx, ids.u1, []UserSymbol{
		{SymbolCode: "AAPL", SortKey: 0},
		{SymbolCode: "GOOGL", SortKey: 1},
	})
	assert.ErrorIs(t, err, ErrReorderCodesMismatch)

	list, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "AAPL", list[0].SymbolCode)
	assert.Equal(t, 0, list[0].SortKey)
	assert.Equal(t, "GOOGL", list[1].SymbolCode)
	assert.Equal(t, 1, list[1].SortKey)
	assert.Equal(t, "MSFT", list[2].SymbolCode)
	assert.Equal(t, 2, list[2].SortKey)
}

func TestWatchlistRepository_ListByUser_Isolation(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u2, SymbolCode: "GOOGL", SortKey: 0}))

	u1List, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)
	require.Len(t, u1List, 1)
	assert.Equal(t, "AAPL", u1List[0].SymbolCode)

	u2List, err := repo.ListByUser(ctx, ids.u2)
	require.NoError(t, err)
	require.Len(t, u2List, 1)
	assert.Equal(t, "GOOGL", u2List[0].SymbolCode)
}
