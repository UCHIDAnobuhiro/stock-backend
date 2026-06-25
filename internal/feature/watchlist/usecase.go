package watchlist

import (
	"context"
	"fmt"
	"log/slog"
)

// Repository はウォッチリスト操作の永続化層を抽象化します。
type Repository interface {
	// ListByUser はユーザーのウォッチリストを sort_key 昇順で返します。
	ListByUser(ctx context.Context, userID int64) ([]UserSymbol, error)
	// Add はsort_keyを指定してウォッチリストに銘柄を追加します。
	// 重複エントリは ErrAlreadyInWatchlist、銘柄コードの FK 違反は ErrSymbolNotFound を返します。
	Add(ctx context.Context, entry UserSymbol) error
	// AddWithNextSortKey はsort_keyをトランザクション内でMAX+1採番して銘柄を追加します。
	// MaxSortKey取得とInsertをアトミックに実行するため、並行追加時の重複順位を防ぎます。
	AddWithNextSortKey(ctx context.Context, userID int64, symbolCode string) error
	// Remove はウォッチリストから銘柄を削除します。
	// 対象がウォッチリストに存在しない場合は ErrNotInWatchlist を返します。
	Remove(ctx context.Context, userID int64, symbolCode string) error
	// UpdateSortKeys は entries の SortKey でウォッチリストの並び順を一括更新します。
	// 全更新は原子的に適用され、途中で失敗した場合はいずれの並び順も変更されません。
	UpdateSortKeys(ctx context.Context, userID int64, entries []UserSymbol) error
}

// SymbolExistsChecker は銘柄の存在確認を行うインターフェースです。
// watchlist usecase が symbollist feature に直接依存しないよう、
// 最小限の読み取り専用インターフェースをここで定義します。
type SymbolExistsChecker interface {
	Exists(ctx context.Context, code string) (bool, error)
}

// usecase はウォッチリスト操作のビジネスロジックを提供します。
type usecase struct {
	repo          Repository
	symbolChecker SymbolExistsChecker
}

// NewUsecase は指定されたリポジトリと銘柄チェッカーで usecase の新しいインスタンスを生成します。
func NewUsecase(repo Repository, symbolChecker SymbolExistsChecker) *usecase {
	return &usecase{repo: repo, symbolChecker: symbolChecker}
}

// ListUserSymbols はユーザーのウォッチリストをソート順で返します。
func (u *usecase) ListUserSymbols(ctx context.Context, userID int64) ([]UserSymbol, error) {
	return u.repo.ListByUser(ctx, userID)
}

// AddSymbol はウォッチリストに銘柄を追加します。
// symbols テーブルに存在しない銘柄コードの場合は ErrSymbolNotFound を返します。
// 既にウォッチリストに存在する場合は ErrAlreadyInWatchlist を返します。
func (u *usecase) AddSymbol(ctx context.Context, userID int64, symbolCode string) error {
	exists, err := u.symbolChecker.Exists(ctx, symbolCode)
	if err != nil {
		return fmt.Errorf("checking symbol existence: %w", err)
	}
	if !exists {
		return ErrSymbolNotFound
	}

	return u.repo.AddWithNextSortKey(ctx, userID, symbolCode)
}

// RemoveSymbol はウォッチリストから銘柄を削除します。
func (u *usecase) RemoveSymbol(ctx context.Context, userID int64, symbolCode string) error {
	return u.repo.Remove(ctx, userID, symbolCode)
}

// ReorderSymbols はウォッチリストの並び順を更新します。
// orderedCodes はユーザーの watchlist 全件と過不足・重複なく一致する必要があります。
// 一致しない場合は ErrReorderCodesMismatch を返します。これは部分リストを渡した際に
// 更新対象外レコードと sort_key が衝突し、(user_id, sort_key) UNIQUE 制約違反で
// トランザクションが失敗する（500）のを防ぐためです。
func (u *usecase) ReorderSymbols(ctx context.Context, userID int64, orderedCodes []string) error {
	existing, err := u.repo.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("listing watchlist: %w", err)
	}

	if len(orderedCodes) != len(existing) {
		return ErrReorderCodesMismatch
	}

	existingCodes := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		existingCodes[e.SymbolCode] = struct{}{}
	}

	seen := make(map[string]struct{}, len(orderedCodes))
	for _, code := range orderedCodes {
		if _, dup := seen[code]; dup {
			return ErrReorderCodesMismatch
		}
		if _, ok := existingCodes[code]; !ok {
			return ErrReorderCodesMismatch
		}
		seen[code] = struct{}{}
	}

	entries := make([]UserSymbol, 0, len(orderedCodes))
	for i, code := range orderedCodes {
		entries = append(entries, UserSymbol{
			UserID:     userID,
			SymbolCode: code,
			SortKey:    i,
		})
	}
	return u.repo.UpdateSortKeys(ctx, userID, entries)
}

// OnUserCreated は PostSignupHook インターフェースを実装します。
// サインアップ直後に呼ばれ、デフォルト銘柄を追加します。
func (u *usecase) OnUserCreated(ctx context.Context, userID int64) error {
	return u.InitializeDefaults(ctx, userID)
}

// InitializeDefaults は新規ユーザー向けにデフォルト銘柄（AAPL/MSFT/GOOGL）を追加します。
// symbols テーブルに存在する銘柄のみ追加します（存在しない場合はスキップ）。
func (u *usecase) InitializeDefaults(ctx context.Context, userID int64) error {
	defaultSymbols := []string{"AAPL", "MSFT", "GOOGL"}

	for i, code := range defaultSymbols {
		exists, err := u.symbolChecker.Exists(ctx, code)
		if err != nil {
			return fmt.Errorf("checking symbol %s: %w", code, err)
		}
		if !exists {
			slog.Warn("default symbol not found in symbols table, skipping", "code", code)
			continue
		}
		if err := u.repo.Add(ctx, UserSymbol{
			UserID:     userID,
			SymbolCode: code,
			SortKey:    i,
		}); err != nil {
			return fmt.Errorf("adding default symbol %s: %w", code, err)
		}
	}
	return nil
}
