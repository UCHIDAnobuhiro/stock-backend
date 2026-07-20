package candleshttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
)

// mockUsecase はusecaseインターフェースのモック実装です。
type mockUsecase struct {
	GetCandlesFunc func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error)
}

func (m *mockUsecase) GetCandles(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
	return m.GetCandlesFunc(ctx, symbol, interval, outputsize)
}

// TestCandlesHandler_GetCandlesHandler はGetCandlesHandlerのHTTPリクエスト/レスポンス処理をテストします。
func TestCandlesHandler_GetCandlesHandler(t *testing.T) {
	// テスト用の固定時刻
	testTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		url            string
		mockGetCandles func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error)
		expectedStatus int
		expectedBody   string // JSON文字列として比較
	}{
		{
			name: "success: all parameters specified",
			url:  "/candles/7203.T?interval=1day&outputsize=10",
			mockGetCandles: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				assert.Equal(t, "7203.T", symbol)
				assert.Equal(t, "1day", interval)
				assert.Equal(t, 10, outputsize)
				return []candles.Candle{
					{Time: testTime, Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"time":"2023-01-01","open":100,"high":110,"low":90,"close":105,"volume":1000}]`,
		},
		{
			name: "success: default parameter values",
			url:  "/candles/7203.T",
			mockGetCandles: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				assert.Equal(t, "7203.T", symbol)
				assert.Equal(t, "1day", interval) // デフォルト値
				assert.Equal(t, 200, outputsize)  // デフォルト値
				return []candles.Candle{}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
		{
			name: "error: usecase returns error",
			url:  "/candles/9999.T",
			mockGetCandles: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return nil, errors.New("internal server error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
		{
			name:           "error: invalid outputsize string returns 400",
			url:            "/candles/7203.T?outputsize=invalid",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"outputsize must be an integer"}`,
		},
		{
			name:           "error: unsupported interval returns 400",
			url:            "/candles/7203.T?interval=3day",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"unsupported interval"}`,
		},
		{
			name:           "error: empty interval returns 400",
			url:            "/candles/7203.T?interval=",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"unsupported interval"}`,
		},
		{
			name:           "error: symbol code with invalid characters returns 400",
			url:            "/candles/7203%26T",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
		{
			name:           "error: symbol code longer than 20 characters returns 400",
			url:            "/candles/AAAAAAAAAAAAAAAAAAAAA",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
		{
			name:           "error: outputsize zero returns 400",
			url:            "/candles/7203.T?outputsize=0",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"outputsize must be between 1 and 5000"}`,
		},
		{
			name:           "error: negative outputsize returns 400",
			url:            "/candles/7203.T?outputsize=-1",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"outputsize must be between 1 and 5000"}`,
		},
		{
			name:           "error: outputsize exceeding max returns 400",
			url:            "/candles/7203.T?outputsize=5001",
			mockGetCandles: nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"outputsize must be between 1 and 5000"}`,
		},
		{
			name: "success: outputsize lower boundary (1)",
			url:  "/candles/7203.T?outputsize=1",
			mockGetCandles: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				assert.Equal(t, 1, outputsize)
				return []candles.Candle{}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
		{
			name: "success: outputsize upper boundary (5000)",
			url:  "/candles/7203.T?outputsize=5000",
			mockGetCandles: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				assert.Equal(t, 5000, outputsize)
				return []candles.Candle{}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// モックusecaseのインスタンスを生成
			mockUC := &mockUsecase{
				GetCandlesFunc: tt.mockGetCandles,
			}

			h := candleshttp.NewHandler(mockUC)

			router := chi.NewRouter()
			router.Get("/candles/{code}", h.GetCandlesHandler)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}
