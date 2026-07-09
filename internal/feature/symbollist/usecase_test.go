package symbollist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
)

// mockRepository はRepositoryインターフェースのモック実装です。
type mockRepository struct {
	ListActiveFunc func(ctx context.Context) ([]symbollist.Symbol, error)
}

// ListActive はモックのListActive関数を呼び出します。
func (m *mockRepository) ListActive(ctx context.Context) ([]symbollist.Symbol, error) {
	if m.ListActiveFunc != nil {
		return m.ListActiveFunc(ctx)
	}
	return nil, nil
}

// TestNewSymbolUsecase はNewUsecaseコンストラクタが正しくインスタンスを生成することを検証します。
func TestNewSymbolUsecase(t *testing.T) {
	t.Parallel()

	mockRepo := &mockRepository{}
	uc := symbollist.NewUsecase(mockRepo)

	assert.NotNil(t, uc, "usecase should not be nil")
}

// TestSymbolUsecase_ListActiveSymbols はListActiveSymbolsメソッドの各種シナリオをテーブル駆動テストで検証します。
func TestSymbolUsecase_ListActiveSymbols(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		mockListActive  func(ctx context.Context) ([]symbollist.Symbol, error)
		expectedSymbols []symbollist.Symbol
		wantErr         bool
		errMsg          string
	}{
		{
			name: "success: returns list of active symbols",
			mockListActive: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{
					{Code: "AAPL", Name: "Apple Inc.", Market: "NASDAQ", IsActive: true},
					{Code: "MSFT", Name: "Microsoft Corporation", Market: "NASDAQ", IsActive: true},
				}, nil
			},
			expectedSymbols: []symbollist.Symbol{
				{Code: "AAPL", Name: "Apple Inc.", Market: "NASDAQ", IsActive: true},
				{Code: "MSFT", Name: "Microsoft Corporation", Market: "NASDAQ", IsActive: true},
			},
			wantErr: false,
		},
		{
			name: "success: returns empty list when no active symbols",
			mockListActive: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{}, nil
			},
			expectedSymbols: []symbollist.Symbol{},
			wantErr:         false,
		},
		{
			name: "success: returns nil when repository returns nil",
			mockListActive: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return nil, nil
			},
			expectedSymbols: nil,
			wantErr:         false,
		},
		{
			name: "failure: repository returns error",
			mockListActive: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return nil, errors.New("database connection failed")
			},
			expectedSymbols: nil,
			wantErr:         true,
			errMsg:          "database connection failed",
		},
		{
			name: "success: returns single symbol",
			mockListActive: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{
					{Code: "NVDA", Name: "NVIDIA Corporation", Market: "NASDAQ", IsActive: true},
				}, nil
			},
			expectedSymbols: []symbollist.Symbol{
				{Code: "NVDA", Name: "NVIDIA Corporation", Market: "NASDAQ", IsActive: true},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockRepo := &mockRepository{
				ListActiveFunc: tt.mockListActive,
			}
			uc := symbollist.NewUsecase(mockRepo)

			symbols, err := uc.ListActiveSymbols(context.Background())

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.EqualError(t, err, tt.errMsg)
				}
				assert.Nil(t, symbols)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSymbols, symbols)
			}
		})
	}
}

// TestSymbolUsecase_ListActiveSymbols_ContextCancellation はコンテキストがキャンセルされた場合にエラーが返されることを検証します。
func TestSymbolUsecase_ListActiveSymbols_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	mockRepo := &mockRepository{
		ListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
			return nil, ctx.Err()
		},
	}
	uc := symbollist.NewUsecase(mockRepo)

	symbols, err := uc.ListActiveSymbols(ctx)

	assert.Error(t, err)
	assert.Nil(t, symbols)
	assert.ErrorIs(t, err, context.Canceled)
}
