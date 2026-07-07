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

func strPtr(s string) *string {
	return &s
}

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

// TestNewSymbolHandler はNewHandlerコンストラクタが正しくインスタンスを生成することを検証します。
func TestNewSymbolHandler(t *testing.T) {
	t.Parallel()

	mockUC := &mockUsecase{}
	h := symbollisthttp.NewHandler(mockUC)

	assert.NotNil(t, h, "handler should not be nil")
}

// TestSymbolHandler_List はListハンドラーの各種シナリオをテーブル駆動テストで検証します。
func TestSymbolHandler_List(t *testing.T) {
	tests := []struct {
		name               string
		mockListActiveFunc func(ctx context.Context) ([]symbollist.Symbol, error)
		expectedStatus     int
		expectedBody       string
	}{
		{
			name: "success: returns list of symbols",
			mockListActiveFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
				return []symbollist.Symbol{
					{Code: "7203.T", Name: "Toyota Motor", Market: "TSE", LogoURL: strPtr("https://api.twelvedata.com/logo/toyota.com"), IsActive: true},
					{Code: "6758.T", Name: "Sony Group", Market: "TSE", IsActive: true},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"code":"7203.T","name":"Toyota Motor","logo_url":"https://api.twelvedata.com/logo/toyota.com"},{"code":"6758.T","name":"Sony Group","logo_url":null}]`,
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
					{Code: "9984.T", Name: "SoftBank Group", Market: "TSE", IsActive: true},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"code":"9984.T","name":"SoftBank Group","logo_url":null}]`,
		},
		{
			name: "failure: usecase returns error",
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

// TestSymbolHandler_List_DTOConversion はレスポンスに公開DTOフィールドのみが含まれ、内部フィールドが公開されないことを検証します。
func TestSymbolHandler_List_DTOConversion(t *testing.T) {
	t.Parallel()

	// レスポンスに公開DTOフィールドのみが含まれることを検証（Market、IsActiveは含まれない）
	mockUC := &mockUsecase{
		ListActiveSymbolsFunc: func(ctx context.Context) ([]symbollist.Symbol, error) {
			return []symbollist.Symbol{
				{
					Code:     "TEST.T",
					Name:     "Test Company",
					Market:   "NYSE",
					LogoURL:  strPtr("https://api.twelvedata.com/logo/test.com"),
					IsActive: true,
				},
			}, nil
		},
	}
	h := symbollisthttp.NewHandler(mockUC)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/symbols", nil)

	h.List(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[{"code":"TEST.T","name":"Test Company","logo_url":"https://api.twelvedata.com/logo/test.com"}]`, w.Body.String())
	// 内部フィールドが公開されていないことを検証
	assert.NotContains(t, w.Body.String(), "NYSE")
	assert.NotContains(t, w.Body.String(), "is_active")
	assert.NotContains(t, w.Body.String(), "sort_key")
}
