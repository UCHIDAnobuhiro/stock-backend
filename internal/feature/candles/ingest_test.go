//go:build !integration

package candles

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ErrDB はデータベースのセンチネルエラーです。
var ErrDB = errors.New("database error")

// ErrMarketAPI はマーケットAPIのセンチネルエラーです。
var ErrMarketAPI = errors.New("market API error")

// mockWriteRepository はWriteRepositoryインターフェースのモック実装です。
type mockWriteRepository struct {
	UpsertBatchFunc func(ctx context.Context, candles []Candle) error
}

// UpsertBatch はUpsertBatchFuncが設定されていればそれを呼び出します。
func (m *mockWriteRepository) UpsertBatch(ctx context.Context, candles []Candle) error {
	if m.UpsertBatchFunc != nil {
		return m.UpsertBatchFunc(ctx, candles)
	}
	return errors.New("UpsertBatchFunc is not implemented")
}

// mockMarketRepository はMarketRepositoryインターフェースのモック実装です。
type mockMarketRepository struct {
	GetTimeSeriesFunc  func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error)
	GetTimeSeriesCalls int
}

func (m *mockMarketRepository) GetTimeSeries(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
	m.GetTimeSeriesCalls++
	if m.GetTimeSeriesFunc != nil {
		return m.GetTimeSeriesFunc(ctx, symbol, interval, outputsize, loc)
	}
	return nil, errors.New("GetTimeSeriesFunc is not implemented")
}

// mockSymbolRepository はSymbolRepositoryインターフェースのモック実装です。
type mockSymbolRepository struct {
	ListActiveSymbolsFunc  func(ctx context.Context) ([]ActiveSymbol, error)
	ListActiveSymbolsCalls int
}

func (m *mockSymbolRepository) ListActiveSymbols(ctx context.Context) ([]ActiveSymbol, error) {
	m.ListActiveSymbolsCalls++
	if m.ListActiveSymbolsFunc != nil {
		return m.ListActiveSymbolsFunc(ctx)
	}
	return nil, errors.New("ListActiveSymbolsFunc is not implemented")
}

// activeSymbolsFromCodes は文字列配列を Asia/Tokyo TZ の ActiveSymbol 配列に変換します（テスト用ヘルパ）。
func activeSymbolsFromCodes(codes []string) []ActiveSymbol {
	out := make([]ActiveSymbol, len(codes))
	for i, c := range codes {
		out[i] = ActiveSymbol{Code: c, Timezone: "Asia/Tokyo"}
	}
	return out
}

// mockRateLimiter はRateLimiterのモック実装です。
type mockRateLimiter struct {
	WaitIfNeededCalls int
	// WaitIfNeededFunc が設定されていれば呼び出す。nil なら nil を返す（待機なし）。
	WaitIfNeededFunc func(ctx context.Context, callCount int) error
}

func (m *mockRateLimiter) WaitIfNeeded(ctx context.Context) error {
	m.WaitIfNeededCalls++
	if m.WaitIfNeededFunc != nil {
		return m.WaitIfNeededFunc(ctx, m.WaitIfNeededCalls)
	}
	return nil
}

// TestDedupCandles は dedupCandles 関数の重複除去ロジックをテストします。
func TestDedupCandles(t *testing.T) {
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	testCases := []struct {
		name     string
		input    []Candle
		wantLen  int
		wantKeys []string // "symbol|interval|unix" 形式で期待するキー一覧
	}{
		{
			name: "重複なしの場合は全件返す",
			input: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: base},
				{SymbolCode: "AAPL", Interval: "1day", Time: base.AddDate(0, 0, 1)},
			},
			wantLen: 2,
		},
		{
			name: "同一タイムスタンプの重複は1件に絞る",
			input: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: base},
				{SymbolCode: "AAPL", Interval: "1day", Time: base},
			},
			wantLen: 1,
		},
		{
			name: "symbolが異なれば別エントリとして扱う",
			input: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: base},
				{SymbolCode: "GOOG", Interval: "1day", Time: base},
			},
			wantLen: 2,
		},
		{
			name: "intervalが異なれば別エントリとして扱う",
			input: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: base},
				{SymbolCode: "AAPL", Interval: "1week", Time: base},
			},
			wantLen: 2,
		},
		{
			name:    "空スライスは空スライスを返す",
			input:   []Candle{},
			wantLen: 0,
		},
		{
			name: "元スライスを変更しない（backing array 非共有）",
			input: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: base, Close: 100},
				{SymbolCode: "AAPL", Interval: "1day", Time: base, Close: 200}, // 重複
				{SymbolCode: "AAPL", Interval: "1day", Time: base.AddDate(0, 0, 1), Close: 300},
			},
			wantLen: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 元スライスのコピーを保持して変更されていないか確認
			original := make([]Candle, len(tc.input))
			copy(original, tc.input)

			got := dedupCandles(tc.input)

			if len(got) != tc.wantLen {
				t.Errorf("len=%d, want %d", len(got), tc.wantLen)
			}

			// 元スライスが変更されていないことを確認
			for i, c := range tc.input {
				if c != original[i] {
					t.Errorf("input[%d] was modified: got %+v, want %+v", i, c, original[i])
				}
			}

			// 出力に重複がないことを確認
			seen := make(map[string]struct{})
			for _, c := range got {
				key := fmt.Sprintf("%s|%s|%d", c.SymbolCode, c.Interval, c.Time.Unix())
				if _, exists := seen[key]; exists {
					t.Errorf("duplicate key in output: %s", key)
				}
				seen[key] = struct{}{}
			}
		})
	}
}

// TestIngestUsecase_ingestOne はingestOneメソッドのデータ取得・保存処理をテストします。
func TestIngestUsecase_ingestOne(t *testing.T) {
	ctx := context.Background()
	// 2022-12-31（土）と 2023-01-01（日）は同一 ISO 週（2022-W52）かつ異なる月
	testTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	mockDailyCandles := []Candle{
		{Time: testTime, Open: 100, High: 110, Low: 90, Close: 105},
		{Time: testTime.AddDate(0, 0, -1), Open: 95, High: 105, Low: 85, Close: 100},
	}

	testCases := []struct {
		name                  string
		inputSymbol           string
		inputOutputsize       int
		mockGetTimeSeriesFunc func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error)
		mockUpsertBatchFunc   func(ctx context.Context, candles []Candle) error
		expectedErr           error
		verifyCandles         func(t *testing.T, candles []Candle)
	}{
		{
			name:            "success: data fetch and save succeed",
			inputSymbol:     "AAPL",
			inputOutputsize: 5000,
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				if symbol != "AAPL" {
					t.Errorf("GetTimeSeries called with unexpected symbol: got %s, want AAPL", symbol)
				}
				if interval != "1day" {
					t.Errorf("GetTimeSeries must always be called with interval=1day, got %s", interval)
				}
				return mockDailyCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return nil
			},
			expectedErr: nil,
			// 2日足
			// + 0週足（2022-12-31 は土曜 = 不完全な先頭バケットを除外）
			// + 1月足（2022-12 は 31日開始 = 不完全で除外、2023-01 は 1日開始で保持）
			// = 3件
			verifyCandles: func(t *testing.T, candles []Candle) {
				counts := map[string]int{}
				for _, c := range candles {
					if c.SymbolCode != "AAPL" {
						t.Errorf("candle SymbolCode not set: got %s, want AAPL", c.SymbolCode)
					}
					counts[c.Interval]++
				}
				if counts["1day"] != 2 {
					t.Errorf("1day candle count: got %d, want 2", counts["1day"])
				}
				if counts["1week"] != 0 {
					t.Errorf("1week candle count: got %d, want 0 (incomplete first bucket dropped)", counts["1week"])
				}
				if counts["1month"] != 1 {
					t.Errorf("1month candle count: got %d, want 1 (incomplete December bucket dropped)", counts["1month"])
				}
			},
		},
		{
			name:            "error: MarketRepository returns error",
			inputSymbol:     "GOOG",
			inputOutputsize: 5000,
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return nil, ErrMarketAPI
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				t.Error("UpsertBatch should not be called")
				return nil
			},
			expectedErr: ErrMarketAPI,
		},
		{
			name:            "error: Repository returns error",
			inputSymbol:     "MSFT",
			inputOutputsize: 5000,
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockDailyCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return ErrDB
			},
			expectedErr: ErrDB,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedCandles []Candle
			mockMarket := &mockMarketRepository{
				GetTimeSeriesFunc: tc.mockGetTimeSeriesFunc,
			}
			mockCandle := &mockWriteRepository{
				UpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
					capturedCandles = candles
					return tc.mockUpsertBatchFunc(ctx, candles)
				},
			}
			mockRL := &mockRateLimiter{}
			mockSymbol := &mockSymbolRepository{}

			uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
			err := uc.ingestOne(ctx, ActiveSymbol{Code: tc.inputSymbol, Timezone: "Asia/Tokyo"}, tc.inputOutputsize)

			if tc.expectedErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if !errors.Is(err, tc.expectedErr) {
				t.Fatalf("expected %v, got %v", tc.expectedErr, err)
			}

			if tc.verifyCandles != nil && capturedCandles != nil {
				tc.verifyCandles(t, capturedCandles)
			}

			if mockMarket.GetTimeSeriesCalls != 1 {
				t.Errorf("GetTimeSeries was called %d times, expected 1", mockMarket.GetTimeSeriesCalls)
			}
		})
	}
}

// TestIngestUsecase_IngestAll はIngestAllメソッドの全銘柄処理をテストします。
func TestIngestUsecase_IngestAll(t *testing.T) {
	ctx := context.Background()
	testTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	mockCandles := []Candle{
		{Time: testTime, Open: 100, High: 110, Low: 90, Close: 105},
	}

	testCases := []struct {
		name                       string
		inputSymbols               []string
		listActiveCodesErr         error
		mockGetTimeSeriesFunc      func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error)
		mockUpsertBatchFunc        func(ctx context.Context, candles []Candle) error
		expectedErr                error
		expectedResult             IngestResult
		expectedGetTimeSeriesCalls int
		verifyCalledIntervals      func(t *testing.T, intervals []string)
	}{
		{
			name:         "success: fetch all symbols",
			inputSymbols: []string{"AAPL", "GOOG"},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return nil
			},
			expectedErr:    nil,
			expectedResult: IngestResult{Total: 2, Succeeded: 2, Failed: 0},
			// 2銘柄 × 1回（日足のみ取得）= 2回呼び出し
			expectedGetTimeSeriesCalls: 2,
		},
		{
			name:         "success: single symbol fetch succeeds",
			inputSymbols: []string{"TSLA"},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return nil
			},
			expectedErr:    nil,
			expectedResult: IngestResult{Total: 1, Succeeded: 1, Failed: 0},
			// 1銘柄 × 1回 = 1回呼び出し
			expectedGetTimeSeriesCalls: 1,
		},
		{
			name:         "success: empty symbol list",
			inputSymbols: []string{},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				t.Error("GetTimeSeries should not be called")
				return nil, errors.New("should not be called")
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				t.Error("UpsertBatch should not be called")
				return nil
			},
			expectedErr:                nil,
			expectedResult:             IngestResult{Total: 0, Succeeded: 0, Failed: 0},
			expectedGetTimeSeriesCalls: 0,
		},
		{
			name:         "partial failure: continues processing and aggregates errors",
			inputSymbols: []string{"AAPL", "INVALID", "GOOG"},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				if symbol == "INVALID" {
					return nil, ErrMarketAPI
				}
				return mockCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return nil
			},
			expectedErr:    nil, // 銘柄単位の失敗は IngestResult に集約され、error は返らない
			expectedResult: IngestResult{Total: 3, Succeeded: 2, Failed: 1},
			// 3銘柄 × 1回 = 3回呼び出し（エラーが発生しても全銘柄が試行される）
			expectedGetTimeSeriesCalls: 3,
		},
		{
			name:         "partial failure: UpsertBatch failure is aggregated",
			inputSymbols: []string{"AAPL", "GOOG"},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				if len(candles) > 0 && candles[0].SymbolCode == "AAPL" {
					return ErrDB
				}
				return nil
			},
			expectedErr:    nil,
			expectedResult: IngestResult{Total: 2, Succeeded: 1, Failed: 1},
			// 2銘柄 × 1回 = 2回呼び出し
			expectedGetTimeSeriesCalls: 2,
		},
		{
			name:         "success: only fetches 1day interval from API",
			inputSymbols: []string{"AAPL", "GOOG"},
			mockGetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockCandles, nil
			},
			mockUpsertBatchFunc: func(ctx context.Context, candles []Candle) error {
				return nil
			},
			expectedErr:                nil,
			expectedResult:             IngestResult{Total: 2, Succeeded: 2, Failed: 0},
			expectedGetTimeSeriesCalls: 2,
			verifyCalledIntervals: func(t *testing.T, intervals []string) {
				for i, interval := range intervals {
					if interval != "1day" {
						t.Errorf("call[%d]: interval=%s, want 1day", i, interval)
					}
				}
			},
		},
		{
			name:                       "fatal: ListActiveCodes returns error",
			inputSymbols:               nil,
			listActiveCodesErr:         ErrDB,
			expectedErr:                ErrDB,
			expectedResult:             IngestResult{}, // 部分集計なし
			expectedGetTimeSeriesCalls: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var calledIntervals []string
			mockMarket := &mockMarketRepository{
				GetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
					calledIntervals = append(calledIntervals, interval)
					return tc.mockGetTimeSeriesFunc(ctx, symbol, interval, outputsize, loc)
				},
			}
			mockCandle := &mockWriteRepository{
				UpsertBatchFunc: tc.mockUpsertBatchFunc,
			}
			mockSymbol := &mockSymbolRepository{
				ListActiveSymbolsFunc: func(ctx context.Context) ([]ActiveSymbol, error) {
					if tc.listActiveCodesErr != nil {
						return nil, tc.listActiveCodesErr
					}
					return activeSymbolsFromCodes(tc.inputSymbols), nil
				},
			}
			mockRL := &mockRateLimiter{}

			uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
			result, err := uc.IngestAll(ctx)

			if tc.expectedErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if !errors.Is(err, tc.expectedErr) {
				t.Fatalf("expected %v, got %v", tc.expectedErr, err)
			}

			if result.Total != tc.expectedResult.Total {
				t.Errorf("result.Total=%d, want %d", result.Total, tc.expectedResult.Total)
			}
			if result.Succeeded != tc.expectedResult.Succeeded {
				t.Errorf("result.Succeeded=%d, want %d", result.Succeeded, tc.expectedResult.Succeeded)
			}
			if result.Failed != tc.expectedResult.Failed {
				t.Errorf("result.Failed=%d, want %d", result.Failed, tc.expectedResult.Failed)
			}

			if mockMarket.GetTimeSeriesCalls != tc.expectedGetTimeSeriesCalls {
				t.Errorf("GetTimeSeries was called %d times, expected %d", mockMarket.GetTimeSeriesCalls, tc.expectedGetTimeSeriesCalls)
			}

			if tc.verifyCalledIntervals != nil {
				tc.verifyCalledIntervals(t, calledIntervals)
			}
		})
	}
}

// TestIngestUsecase_IngestAll_MidLoopFatal はループ途中で発生する致命的エラー
// （ctx キャンセル、rateLimiter 失敗）が部分集計と共に error を返すことを検証します。
func TestIngestUsecase_IngestAll_MidLoopFatal(t *testing.T) {
	testTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	mockCandles := []Candle{
		{Time: testTime, Open: 100, High: 110, Low: 90, Close: 105},
	}

	t.Run("ctx cancelled after first symbol succeeds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		var processedCount int
		mockMarket := &mockMarketRepository{
			GetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				processedCount++
				if processedCount == 1 {
					// 1銘柄目の処理完了後に ctx をキャンセルし、2銘柄目のループ先頭で検出させる
					cancel()
				}
				return mockCandles, nil
			},
		}
		mockCandle := &mockWriteRepository{
			UpsertBatchFunc: func(ctx context.Context, candles []Candle) error { return nil },
		}
		mockSymbol := &mockSymbolRepository{
			ListActiveSymbolsFunc: func(ctx context.Context) ([]ActiveSymbol, error) {
				return activeSymbolsFromCodes([]string{"AAPL", "GOOG", "MSFT"}), nil
			},
		}
		mockRL := &mockRateLimiter{}

		uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
		result, err := uc.IngestAll(ctx)

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context.Canceled", err)
		}
		// 1銘柄目は成功、2銘柄目で ctx チェックに引っかかり離脱
		if result.Total != 3 {
			t.Errorf("result.Total=%d, want 3", result.Total)
		}
		if result.Succeeded != 1 {
			t.Errorf("result.Succeeded=%d, want 1", result.Succeeded)
		}
		if result.Failed != 0 {
			t.Errorf("result.Failed=%d, want 0", result.Failed)
		}
		// 部分集計は exit コード判定で問題ないこと（Failed=0 なので失敗率は 0）
		if rate := result.FailureRate(); rate != 0 {
			t.Errorf("FailureRate()=%v, want 0 (no symbol-level failures occurred)", rate)
		}
	})

	t.Run("rateLimiter fails on second symbol", func(t *testing.T) {
		ctx := context.Background()
		errRateLimit := errors.New("rate limit exceeded")

		mockMarket := &mockMarketRepository{
			GetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
				return mockCandles, nil
			},
		}
		mockCandle := &mockWriteRepository{
			UpsertBatchFunc: func(ctx context.Context, candles []Candle) error { return nil },
		}
		mockSymbol := &mockSymbolRepository{
			ListActiveSymbolsFunc: func(ctx context.Context) ([]ActiveSymbol, error) {
				return activeSymbolsFromCodes([]string{"AAPL", "GOOG", "MSFT"}), nil
			},
		}
		mockRL := &mockRateLimiter{
			WaitIfNeededFunc: func(ctx context.Context, callCount int) error {
				if callCount == 2 {
					return errRateLimit
				}
				return nil
			},
		}

		uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
		result, err := uc.IngestAll(ctx)

		if !errors.Is(err, errRateLimit) {
			t.Fatalf("err=%v, want errRateLimit", err)
		}
		if result.Total != 3 {
			t.Errorf("result.Total=%d, want 3", result.Total)
		}
		if result.Succeeded != 1 {
			t.Errorf("result.Succeeded=%d, want 1 (only AAPL processed)", result.Succeeded)
		}
		if result.Failed != 0 {
			t.Errorf("result.Failed=%d, want 0", result.Failed)
		}
	})
}

// TestIngestUsecase_ingestOne_InvalidTimezone は不正な TZ 文字列でエラーが返されることを検証します。
func TestIngestUsecase_ingestOne_InvalidTimezone(t *testing.T) {
	ctx := context.Background()
	mockMarket := &mockMarketRepository{}
	mockCandle := &mockWriteRepository{}
	mockSymbol := &mockSymbolRepository{}
	mockRL := &mockRateLimiter{}

	uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
	err := uc.ingestOne(ctx, ActiveSymbol{Code: "AAPL", Timezone: "Not/A_Real_Zone"}, 5000)
	if err == nil {
		t.Fatal("expected error for invalid timezone, got nil")
	}
	if mockMarket.GetTimeSeriesCalls != 0 {
		t.Errorf("GetTimeSeries should not be called when TZ is invalid, got %d calls", mockMarket.GetTimeSeriesCalls)
	}
}

// TestIngestUsecase_ingestOne_PassesLocation は GetTimeSeries に銘柄の TZ で
// 解決された Location が渡されることを検証します。
func TestIngestUsecase_ingestOne_PassesLocation(t *testing.T) {
	ctx := context.Background()
	want, _ := time.LoadLocation("America/New_York")

	var gotLoc *time.Location
	mockMarket := &mockMarketRepository{
		GetTimeSeriesFunc: func(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error) {
			gotLoc = loc
			return nil, nil
		},
	}
	mockCandle := &mockWriteRepository{
		UpsertBatchFunc: func(ctx context.Context, candles []Candle) error { return nil },
	}
	mockSymbol := &mockSymbolRepository{}
	mockRL := &mockRateLimiter{}

	uc := NewIngestUsecase(mockMarket, mockCandle, mockSymbol, mockRL)
	if err := uc.ingestOne(ctx, ActiveSymbol{Code: "AAPL", Timezone: "America/New_York"}, 5000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotLoc == nil || gotLoc.String() != want.String() {
		t.Errorf("GetTimeSeries received loc=%v, want %v", gotLoc, want)
	}
}

// TestIngestResult_FailureRate は FailureRate の境界条件を検証します。
func TestIngestResult_FailureRate(t *testing.T) {
	testCases := []struct {
		name   string
		result IngestResult
		want   float64
	}{
		{name: "Total=0 returns 0", result: IngestResult{Total: 0, Failed: 0}, want: 0},
		{name: "all succeeded", result: IngestResult{Total: 10, Failed: 0}, want: 0},
		{name: "all failed", result: IngestResult{Total: 10, Failed: 10}, want: 1.0},
		{name: "20% failure", result: IngestResult{Total: 10, Failed: 2}, want: 0.2},
		{name: "50% failure", result: IngestResult{Total: 4, Failed: 2}, want: 0.5},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.FailureRate(); got != tc.want {
				t.Errorf("FailureRate()=%v, want %v", got, tc.want)
			}
		})
	}
}
