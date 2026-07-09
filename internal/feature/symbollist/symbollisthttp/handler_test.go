package symbollisthttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/symbollisthttp"
)

// mockUsecase はUsecaseインターフェースのモック実装です。
type mockUsecase struct {
	ListActiveSymbolsFunc func(ctx context.Context) ([]symbollist.Symbol, error)
}

// ListActiveSymbols はモックのListActiveSymbols関数を呼び出します。
func (m *mockUsecase) ListActiveSymbols(ctx context.Context) ([]symbollist.Symbol, error) {
	if m.ListActiveSymbolsFunc != nil {
		return m.ListActiveSymbolsFunc(ctx)
	}
	return nil, nil
}

// TestSymbolHandler_List はListハンドラーの各種シナリオをテーブル駆動テストで検証します。
func TestSymbolHandler_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mockListActiveFunc func(ctx context.Context) ([]symbollist.Symbol, error)
		expectedStatus     int
		expectedBody       string
	}{
		{
			// expectedBodyとの完全一致検証により、内部フィールド（Market, IsActive）が公開されないことも担保する
			name: "success: returns list of symbols",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{
					{Code: "AAPL", Name: "Apple Inc.", Market: "NASDAQ", LogoURL: new("https://api.twelvedata.com/logo/apple.com"), IsActive: true},
					{Code: "MSFT", Name: "Microsoft Corporation", Market: "NASDAQ", IsActive: true},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"code":"AAPL","name":"Apple Inc.","logo_url":"https://api.twelvedata.com/logo/apple.com"},{"code":"MSFT","name":"Microsoft Corporation","logo_url":null}]`,
		},
		{
			name: "success: returns empty list when no symbols",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
		{
			name: "success: returns single symbol",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{
					{Code: "NVDA", Name: "NVIDIA Corporation", Market: "NASDAQ", IsActive: true},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"code":"NVDA","name":"NVIDIA Corporation","logo_url":null}]`,
		},
		{
			name: "error: usecase returns error",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return nil, errors.New("database connection failed")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
		{
			name: "success: returns nil from usecase",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return nil, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{
				ListActiveSymbolsFunc: tt.mockListActiveFunc,
			}
			h := symbollisthttp.NewHandler(mockUC)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/symbols", nil)

			h.List(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}
