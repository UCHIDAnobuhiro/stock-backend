//go:build !integration

package watchlist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
)

// mockRepository はRepositoryインターフェースのモック実装です。
type mockRepository struct {
	ListByUserFunc         func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error)
	AddFunc                func(ctx context.Context, entry watchlist.UserSymbol) error
	AddWithNextSortKeyFunc func(ctx context.Context, userID int64, symbolCode string) error
	RemoveFunc             func(ctx context.Context, userID int64, symbolCode string) error
	UpdateSortKeysFunc     func(ctx context.Context, userID int64, entries []watchlist.UserSymbol) error

	AddedEntries    []watchlist.UserSymbol
	UpdatedEntries  []watchlist.UserSymbol
	AddWithNextArgs []struct {
		UserID     int64
		SymbolCode string
	}
	RemoveArgs []struct {
		UserID     int64
		SymbolCode string
	}
	AddWithNextCalls int
	RemoveCalls      int
}

func (m *mockRepository) ListByUser(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
	if m.ListByUserFunc != nil {
		return m.ListByUserFunc(ctx, userID)
	}
	return nil, nil
}

func (m *mockRepository) Add(ctx context.Context, entry watchlist.UserSymbol) error {
	m.AddedEntries = append(m.AddedEntries, entry)
	if m.AddFunc != nil {
		return m.AddFunc(ctx, entry)
	}
	return nil
}

func (m *mockRepository) AddWithNextSortKey(ctx context.Context, userID int64, symbolCode string) error {
	m.AddWithNextCalls++
	m.AddWithNextArgs = append(m.AddWithNextArgs, struct {
		UserID     int64
		SymbolCode string
	}{userID, symbolCode})
	if m.AddWithNextSortKeyFunc != nil {
		return m.AddWithNextSortKeyFunc(ctx, userID, symbolCode)
	}
	return nil
}

func (m *mockRepository) Remove(ctx context.Context, userID int64, symbolCode string) error {
	m.RemoveCalls++
	m.RemoveArgs = append(m.RemoveArgs, struct {
		UserID     int64
		SymbolCode string
	}{userID, symbolCode})
	if m.RemoveFunc != nil {
		return m.RemoveFunc(ctx, userID, symbolCode)
	}
	return nil
}

func (m *mockRepository) UpdateSortKeys(ctx context.Context, userID int64, entries []watchlist.UserSymbol) error {
	m.UpdatedEntries = entries
	if m.UpdateSortKeysFunc != nil {
		return m.UpdateSortKeysFunc(ctx, userID, entries)
	}
	return nil
}

// mockSymbolExistsChecker はSymbolExistsCheckerインターフェースのモック実装です。
type mockSymbolExistsChecker struct {
	ExistsFunc   func(ctx context.Context, code string) (bool, error)
	CheckedCodes []string
}

func (m *mockSymbolExistsChecker) Exists(ctx context.Context, code string) (bool, error) {
	m.CheckedCodes = append(m.CheckedCodes, code)
	if m.ExistsFunc != nil {
		return m.ExistsFunc(ctx, code)
	}
	return false, nil
}

func TestNewWatchlistUsecase(t *testing.T) {
	t.Parallel()

	uc := watchlist.NewUsecase(&mockRepository{}, &mockSymbolExistsChecker{})

	assert.NotNil(t, uc, "usecase should not be nil")
}

func TestWatchlistUsecase_ListUserSymbols(t *testing.T) {
	t.Parallel()

	repoErr := errors.New("database connection failed")
	tests := []struct {
		name        string
		listByUser  func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error)
		wantSymbols []watchlist.UserSymbol
		wantErr     bool
		errMsg      string
	}{
		{
			name: "success: returns user watchlist",
			listByUser: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return []watchlist.UserSymbol{
					{ID: 1, UserID: userID, SymbolCode: "AAPL", SortKey: 0},
					{ID: 2, UserID: userID, SymbolCode: "MSFT", SortKey: 1},
				}, nil
			},
			wantSymbols: []watchlist.UserSymbol{
				{ID: 1, UserID: 42, SymbolCode: "AAPL", SortKey: 0},
				{ID: 2, UserID: 42, SymbolCode: "MSFT", SortKey: 1},
			},
			wantErr: false,
		},
		{
			name: "success: returns empty watchlist",
			listByUser: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return []watchlist.UserSymbol{}, nil
			},
			wantSymbols: []watchlist.UserSymbol{},
			wantErr:     false,
		},
		{
			name: "failure: repository returns error",
			listByUser: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return nil, repoErr
			},
			wantSymbols: nil,
			wantErr:     true,
			errMsg:      "database connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &mockRepository{ListByUserFunc: tt.listByUser}
			uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

			symbols, err := uc.ListUserSymbols(context.Background(), 42)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, repoErr)
				if tt.errMsg != "" {
					assert.EqualError(t, err, tt.errMsg)
				}
				assert.Nil(t, symbols)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantSymbols, symbols)
			}
		})
	}
}

func TestWatchlistUsecase_AddSymbol(t *testing.T) {
	t.Parallel()

	checkerErr := errors.New("checker down")
	addErr := errors.New("insert failed")
	tests := []struct {
		name             string
		exists           func(ctx context.Context, code string) (bool, error)
		addWithNext      func(ctx context.Context, userID int64, symbolCode string) error
		wantErr          bool
		wantErrIs        error
		wantErrContains  string
		wantAddWithCalls int
	}{
		{
			name: "success: existing symbol is added",
			exists: func(ctx context.Context, code string) (bool, error) {
				return true, nil
			},
			wantErr:          false,
			wantAddWithCalls: 1,
		},
		{
			name: "failure: symbol does not exist returns ErrSymbolNotFound",
			exists: func(ctx context.Context, code string) (bool, error) {
				return false, nil
			},
			wantErr:          true,
			wantErrIs:        watchlist.ErrSymbolNotFound,
			wantAddWithCalls: 0,
		},
		{
			name: "failure: existence check returns wrapped error",
			exists: func(ctx context.Context, code string) (bool, error) {
				return false, checkerErr
			},
			wantErr:          true,
			wantErrIs:        checkerErr,
			wantErrContains:  "checking symbol existence",
			wantAddWithCalls: 0,
		},
		{
			name: "failure: repository add returns error",
			exists: func(ctx context.Context, code string) (bool, error) {
				return true, nil
			},
			addWithNext: func(ctx context.Context, userID int64, symbolCode string) error {
				return addErr
			},
			wantErr:          true,
			wantErrIs:        addErr,
			wantErrContains:  "insert failed",
			wantAddWithCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &mockRepository{AddWithNextSortKeyFunc: tt.addWithNext}
			checker := &mockSymbolExistsChecker{ExistsFunc: tt.exists}
			uc := watchlist.NewUsecase(repo, checker)

			err := uc.AddSymbol(context.Background(), 42, "AAPL")

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrIs != nil {
					assert.ErrorIs(t, err, tt.wantErrIs)
				}
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantAddWithCalls, repo.AddWithNextCalls)
			assert.Equal(t, []string{"AAPL"}, checker.CheckedCodes)
			if tt.wantAddWithCalls == 1 {
				require.Len(t, repo.AddWithNextArgs, 1)
				assert.Equal(t, int64(42), repo.AddWithNextArgs[0].UserID)
				assert.Equal(t, "AAPL", repo.AddWithNextArgs[0].SymbolCode)
			} else {
				assert.Empty(t, repo.AddWithNextArgs)
			}
		})
	}
}

func TestWatchlistUsecase_RemoveSymbol(t *testing.T) {
	t.Parallel()

	removeErr := errors.New("delete failed")
	tests := []struct {
		name    string
		remove  func(ctx context.Context, userID int64, symbolCode string) error
		wantErr error
	}{
		{
			name:    "success: symbol is removed",
			remove:  func(ctx context.Context, userID int64, symbolCode string) error { return nil },
			wantErr: nil,
		},
		{
			name: "failure: repository returns error",
			remove: func(ctx context.Context, userID int64, symbolCode string) error {
				return removeErr
			},
			wantErr: removeErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &mockRepository{RemoveFunc: tt.remove}
			uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

			err := uc.RemoveSymbol(context.Background(), 42, "AAPL")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, 1, repo.RemoveCalls)
			require.Len(t, repo.RemoveArgs, 1)
			assert.Equal(t, int64(42), repo.RemoveArgs[0].UserID)
			assert.Equal(t, "AAPL", repo.RemoveArgs[0].SymbolCode)
		})
	}
}

func TestWatchlistUsecase_ReorderSymbols(t *testing.T) {
	t.Parallel()

	// listByUserFunc は指定したコード群を sort_key 昇順の watchlist として返すヘルパーです。
	listByUserFunc := func(codes ...string) func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
		return func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
			entries := make([]watchlist.UserSymbol, 0, len(codes))
			for i, code := range codes {
				entries = append(entries, watchlist.UserSymbol{UserID: userID, SymbolCode: code, SortKey: i})
			}
			return entries, nil
		}
	}

	t.Run("success: assigns sequential sort keys in order", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{ListByUserFunc: listByUserFunc("AAPL", "MSFT", "GOOGL")}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"MSFT", "AAPL", "GOOGL"})

		require.NoError(t, err)
		assert.Equal(t, []watchlist.UserSymbol{
			{UserID: 42, SymbolCode: "MSFT", SortKey: 0},
			{UserID: 42, SymbolCode: "AAPL", SortKey: 1},
			{UserID: 42, SymbolCode: "GOOGL", SortKey: 2},
		}, repo.UpdatedEntries)
	})

	t.Run("success: empty order with empty watchlist produces empty entries", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{ListByUserFunc: listByUserFunc()}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{})

		require.NoError(t, err)
		assert.Empty(t, repo.UpdatedEntries)
	})

	t.Run("failure: partial list returns ErrReorderCodesMismatch", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{ListByUserFunc: listByUserFunc("AAPL", "MSFT", "GOOGL")}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"GOOGL", "AAPL"})

		assert.ErrorIs(t, err, watchlist.ErrReorderCodesMismatch)
		assert.Nil(t, repo.UpdatedEntries)
	})

	t.Run("failure: unknown code returns ErrReorderCodesMismatch", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{ListByUserFunc: listByUserFunc("AAPL", "MSFT")}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"AAPL", "TSLA"})

		assert.ErrorIs(t, err, watchlist.ErrReorderCodesMismatch)
		assert.Nil(t, repo.UpdatedEntries)
	})

	t.Run("failure: duplicate code returns ErrReorderCodesMismatch", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{ListByUserFunc: listByUserFunc("AAPL", "MSFT")}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"AAPL", "AAPL"})

		assert.ErrorIs(t, err, watchlist.ErrReorderCodesMismatch)
		assert.Nil(t, repo.UpdatedEntries)
	})

	listErr := errors.New("list failed")
	updateErr := errors.New("update failed")

	t.Run("failure: ListByUser returns error", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{
			ListByUserFunc: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return nil, listErr
			},
		}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"AAPL"})

		assert.ErrorContains(t, err, "list failed")
		assert.ErrorIs(t, err, listErr)
	})

	t.Run("failure: repository returns error", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{
			ListByUserFunc: listByUserFunc("AAPL"),
			UpdateSortKeysFunc: func(ctx context.Context, userID int64, entries []watchlist.UserSymbol) error {
				return updateErr
			},
		}
		uc := watchlist.NewUsecase(repo, &mockSymbolExistsChecker{})

		err := uc.ReorderSymbols(context.Background(), 42, []string{"AAPL"})

		assert.EqualError(t, err, "update failed")
		assert.ErrorIs(t, err, updateErr)
	})
}

func TestWatchlistUsecase_InitializeDefaults(t *testing.T) {
	t.Parallel()
	checkerErr := errors.New("checker down")
	addErr := errors.New("insert failed")

	t.Run("success: adds all default symbols when they exist", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) { return true, nil },
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.NoError(t, err)
		assert.Equal(t, []watchlist.UserSymbol{
			{UserID: 7, SymbolCode: "AAPL", SortKey: 0},
			{UserID: 7, SymbolCode: "MSFT", SortKey: 1},
			{UserID: 7, SymbolCode: "GOOGL", SortKey: 2},
		}, repo.AddedEntries)
	})

	t.Run("success: skips symbols that do not exist", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) {
				return code == "MSFT", nil
			},
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.NoError(t, err)
		assert.Equal(t, []watchlist.UserSymbol{
			{UserID: 7, SymbolCode: "MSFT", SortKey: 1},
		}, repo.AddedEntries)
	})

	t.Run("failure: existence check returns wrapped error", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) {
				return false, checkerErr
			},
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.Error(t, err)
		assert.ErrorContains(t, err, "checking symbol AAPL")
		assert.ErrorIs(t, err, checkerErr)
		assert.Empty(t, repo.AddedEntries)
	})

	t.Run("failure: repository add returns wrapped error", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{
			AddFunc: func(ctx context.Context, entry watchlist.UserSymbol) error {
				return addErr
			},
		}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) { return true, nil },
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.Error(t, err)
		assert.ErrorContains(t, err, "adding default symbol AAPL")
		assert.ErrorIs(t, err, addErr)
	})

	t.Run("failure: second existence check stops before third symbol", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) {
				if code == "MSFT" {
					return false, checkerErr
				}
				return true, nil
			},
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.Error(t, err)
		assert.ErrorIs(t, err, checkerErr)
		assert.Equal(t, []string{"AAPL", "MSFT"}, checker.CheckedCodes)
		assert.Equal(t, []watchlist.UserSymbol{{UserID: 7, SymbolCode: "AAPL", SortKey: 0}}, repo.AddedEntries)
	})

	t.Run("failure: second add stops before third symbol", func(t *testing.T) {
		t.Parallel()

		repo := &mockRepository{
			AddFunc: func(ctx context.Context, entry watchlist.UserSymbol) error {
				if entry.SymbolCode == "MSFT" {
					return addErr
				}
				return nil
			},
		}
		checker := &mockSymbolExistsChecker{
			ExistsFunc: func(ctx context.Context, code string) (bool, error) { return true, nil },
		}
		uc := watchlist.NewUsecase(repo, checker)

		err := uc.InitializeDefaults(context.Background(), 7)

		require.Error(t, err)
		assert.ErrorIs(t, err, addErr)
		assert.Equal(t, []string{"AAPL", "MSFT"}, checker.CheckedCodes)
		assert.Equal(t, []watchlist.UserSymbol{
			{UserID: 7, SymbolCode: "AAPL", SortKey: 0},
			{UserID: 7, SymbolCode: "MSFT", SortKey: 1},
		}, repo.AddedEntries)
	})
}

func TestWatchlistUsecase_OnUserCreated(t *testing.T) {
	t.Parallel()

	repo := &mockRepository{}
	checker := &mockSymbolExistsChecker{
		ExistsFunc: func(ctx context.Context, code string) (bool, error) { return true, nil },
	}
	uc := watchlist.NewUsecase(repo, checker)

	err := uc.OnUserCreated(context.Background(), 7)

	require.NoError(t, err)
	assert.Equal(t, []watchlist.UserSymbol{
		{UserID: 7, SymbolCode: "AAPL", SortKey: 0},
		{UserID: 7, SymbolCode: "MSFT", SortKey: 1},
		{UserID: 7, SymbolCode: "GOOGL", SortKey: 2},
	}, repo.AddedEntries)
}
