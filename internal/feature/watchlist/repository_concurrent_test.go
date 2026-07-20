package watchlist

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sortPermutations は3銘柄コードの全並び替え（3! = 6通り）です。
// 並行実行するgoroutine数（8）がこれより多い場合は循環して再利用します。
var sortPermutations = [][]string{
	{"AAPL", "GOOGL", "MSFT"},
	{"AAPL", "MSFT", "GOOGL"},
	{"GOOGL", "AAPL", "MSFT"},
	{"GOOGL", "MSFT", "AAPL"},
	{"MSFT", "AAPL", "GOOGL"},
	{"MSFT", "GOOGL", "AAPL"},
}

// entriesFromPerm はコードの並びから、usecase.ReorderSymbolsと同じ規約（0始まりの連番）で
// SortKeyを割り当てたUserSymbolのスライスを生成します。
func entriesFromPerm(userID int64, codes []string) []UserSymbol {
	entries := make([]UserSymbol, len(codes))
	for i, code := range codes {
		entries[i] = UserSymbol{UserID: userID, SymbolCode: code, SortKey: i}
	}
	return entries
}

// TestWatchlistRepository_UpdateSortKeys_Concurrent は同一ユーザーに対して
// UpdateSortKeysを並行に呼び出しても、LockWatchlistByUserによる行ロック（取得順ロック）で
// デッドロックやユニーク制約違反、件数不一致エラーなく直列化されることを検証します。
func TestWatchlistRepository_UpdateSortKeys_Concurrent(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	// u1・u2 両方に同じ初期状態（AAPL, GOOGL, MSFT の順）を投入する。
	// u2 は並行更新の対象外とし、最後に「無関係のユーザーには影響しない」ことを検証する。
	for _, uid := range []int64{ids.u1, ids.u2} {
		require.NoError(t, repo.Add(ctx, UserSymbol{UserID: uid, SymbolCode: "AAPL", SortKey: 0}))
		require.NoError(t, repo.Add(ctx, UserSymbol{UserID: uid, SymbolCode: "GOOGL", SortKey: 1}))
		require.NoError(t, repo.Add(ctx, UserSymbol{UserID: uid, SymbolCode: "MSFT", SortKey: 2}))
	}

	const goroutines = 8

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			perm := sortPermutations[idx%len(sortPermutations)]
			errs[idx] = repo.UpdateSortKeys(ctx, ids.u1, entriesFromPerm(ids.u1, perm))
		}(i)
	}
	wg.Wait()

	// ロックにより直列化されるため、いずれの呼び出しも失敗しないはず。
	for i, err := range errs {
		assert.NoErrorf(t, err, "goroutine %d の UpdateSortKeys が失敗した", i)
	}

	// u1: 最終状態は投入した並び替えのいずれかと一致し、sort_keyは0,1,2の連番になっているはず。
	final, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)
	require.Len(t, final, 3)

	gotCodes := []string{final[0].SymbolCode, final[1].SymbolCode, final[2].SymbolCode}
	assert.Contains(t, sortPermutations, gotCodes, "最終的な並びは投入したいずれかの並び替えと一致するはず")
	for i, us := range final {
		assert.Equal(t, i, us.SortKey, "ListByUserはsort_key昇順のため、順序位置=sort_keyになるはず")
	}

	// u2: 並行更新の対象外のため、初期状態のまま変化していないはず。
	u2List, err := repo.ListByUser(ctx, ids.u2)
	require.NoError(t, err)
	require.Len(t, u2List, 3)
	assert.Equal(t, "AAPL", u2List[0].SymbolCode)
	assert.Equal(t, 0, u2List[0].SortKey)
	assert.Equal(t, "GOOGL", u2List[1].SymbolCode)
	assert.Equal(t, 1, u2List[1].SortKey)
	assert.Equal(t, "MSFT", u2List[2].SymbolCode)
	assert.Equal(t, 2, u2List[2].SortKey)
}

// alwaysExistsSymbolChecker はSymbolExistsCheckerの常にtrueを返すテスト用スタブです。
// このテストではReorderSymbols/RemoveSymbolのみを検証対象とし、いずれも
// SymbolExistsCheckerを使わないため、呼び出されるかどうかは問いません。
type alwaysExistsSymbolChecker struct{}

func (alwaysExistsSymbolChecker) Exists(ctx context.Context, code string) (bool, error) {
	return true, nil
}

// TestWatchlistUsecase_ReorderSymbols_ConcurrentRemove は ReorderSymbols の連続呼び出しと
// Remove を並行実行した際、usecase/repositoryが返すエラーがドメインエラー
// （ErrReorderCodesMismatch / ErrNotInWatchlist）の範囲に収まり、生のPGエラー
// （unique violation等）が漏れ出さないことを検証します。
//
// ReorderSymbolsはListByUserで取得した現在の一覧とentriesの個数・コードを比較するため、
// Remove（MSFT削除）が割り込むと「現在の一覧（2件）」と「投入したentries（3件）」の
// 個数不一致によりErrReorderCodesMismatchを返す（usecase層のバリデーションで弾かれる）。
// これに加え、usecaseのListByUser呼び出し後・repository.UpdateSortKeysのロック取得前に
// Removeが割り込んだ場合は、repository層のLockWatchlistByUserによる件数チェックでも
// 同じErrReorderCodesMismatchが返る。
func TestWatchlistUsecase_ReorderSymbols_ConcurrentRemove(t *testing.T) {
	t.Parallel()
	db, ids := setupTestDB(t)
	repo := NewRepository(db)
	uc := NewUsecase(repo, alwaysExistsSymbolChecker{})
	ctx := context.Background()

	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "AAPL", SortKey: 0}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "GOOGL", SortKey: 1}))
	require.NoError(t, repo.Add(ctx, UserSymbol{UserID: ids.u1, SymbolCode: "MSFT", SortKey: 2}))

	const reorderIterations = 20

	var (
		wg      sync.WaitGroup
		errMu   sync.Mutex
		allErrs []error
	)
	recordErr := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		allErrs = append(allErrs, err)
	}

	// ReorderSymbolsを繰り返し呼ぶgoroutine。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < reorderIterations; i++ {
			perm := sortPermutations[i%len(sortPermutations)]
			recordErr(uc.ReorderSymbols(ctx, ids.u1, perm))
		}
	}()

	// MSFTを1回だけ削除するgoroutine。
	wg.Add(1)
	go func() {
		defer wg.Done()
		recordErr(uc.RemoveSymbol(ctx, ids.u1, "MSFT"))
	}()

	wg.Wait()

	for i, err := range allErrs {
		if err == nil {
			continue
		}
		assert.Truef(t,
			errors.Is(err, ErrReorderCodesMismatch) || errors.Is(err, ErrNotInWatchlist),
			"呼び出し%dで想定外のエラー: %v（ErrReorderCodesMismatchかErrNotInWatchlistのみ許容）", i, err)
	}

	// 最終状態: MSFTが存在せず、sort_keyに重複がないこと（連番であることまでは要求しない）。
	final, err := repo.ListByUser(ctx, ids.u1)
	require.NoError(t, err)

	seenSortKeys := map[int]struct{}{}
	for _, us := range final {
		assert.NotEqual(t, "MSFT", us.SymbolCode, "MSFTは削除済みのため残っていないはず")
		_, dup := seenSortKeys[us.SortKey]
		assert.Falsef(t, dup, "sort_key %d が重複している", us.SortKey)
		seenSortKeys[us.SortKey] = struct{}{}
	}
}
