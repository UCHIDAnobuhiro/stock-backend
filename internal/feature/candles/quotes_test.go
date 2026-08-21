package candles_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
)

// quoteMockRepository はRepositoryインターフェースのモック実装です。
// GetQuotesは銘柄ごとにFindを呼び出すため、symbolに応じて挙動を切り替えられるようにします。
type quoteMockRepository struct {
	FindFunc func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error)
}

// Find はFindFuncに処理を委譲します。
func (m *quoteMockRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
	return m.FindFunc(ctx, symbol, interval, outputsize)
}

// mkCandle は2024年1月dayの日付・終値closeを持つCandleを生成するテストヘルパーです。
// GetQuotesの計算には Time と Close のみが使われます。
func mkCandle(day int, close float64) candles.Candle {
	return candles.Candle{
		Time:  time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC),
		Close: close,
	}
}

// TestCandlesUsecase_GetQuotes はGetQuotesメソッドの並行呼び出し・変換ロジックをテストします。
func TestCandlesUsecase_GetQuotes(t *testing.T) {
	ctx := context.Background()

	t.Run("success: change and change_percent are computed from the two most recent candles", func(t *testing.T) {
		byCode := map[string][]candles.Candle{
			// Findは時刻降順（[0]が最新）で返す。
			"AAPL":  {mkCandle(2, 105), mkCandle(1, 100)},
			"GOOGL": {mkCandle(2, 210), mkCandle(1, 200)},
		}
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				if interval != "1day" {
					t.Errorf("unexpected interval: got %s, want 1day", interval)
				}
				// bars=0でも前日比計算に2本必要なため outputsize は max(bars, 2) = 2 になる。
				if outputsize != 2 {
					t.Errorf("unexpected outputsize: got %d, want 2", outputsize)
				}
				return byCode[symbol], nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL", "GOOGL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != 2 {
			t.Fatalf("expected 2 quotes, got %d", len(got.Quotes))
		}
		if len(got.Failures) != 0 {
			t.Fatalf("expected no failures, got %+v", got.Failures)
		}

		byResultCode := make(map[string]candles.Quote, len(got.Quotes))
		for _, q := range got.Quotes {
			byResultCode[q.Code] = q
		}

		aaplChange := 105.0 - 100.0
		aaplWantChangePercent := aaplChange / 100.0 * 100
		aapl, ok := byResultCode["AAPL"]
		if !ok {
			t.Fatalf("AAPL not found in result: %+v", got.Quotes)
		}
		if aapl.Close != 105 || aapl.PrevClose != 100 || aapl.Change != aaplChange || aapl.ChangePercent != aaplWantChangePercent {
			t.Errorf("unexpected AAPL quote: %+v", aapl)
		}
		if aapl.Closes != nil {
			t.Errorf("expected Closes to be nil when bars=0, got %v", aapl.Closes)
		}
		if !aapl.Time.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("unexpected AAPL time: %v", aapl.Time)
		}

		googlChange := 210.0 - 200.0
		googlWantChangePercent := googlChange / 200.0 * 100
		googl, ok := byResultCode["GOOGL"]
		if !ok {
			t.Fatalf("GOOGL not found in result: %+v", got.Quotes)
		}
		if googl.Close != 210 || googl.PrevClose != 200 || googl.Change != googlChange || googl.ChangePercent != googlWantChangePercent {
			t.Errorf("unexpected GOOGL quote: %+v", googl)
		}
	})

	t.Run("2本未満のローソク足しかない銘柄は失敗として回収される", func(t *testing.T) {
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				if symbol == "ONE" {
					return []candles.Candle{mkCandle(1, 100)}, nil
				}
				return []candles.Candle{mkCandle(2, 105), mkCandle(1, 100)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL", "ONE"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != 1 || got.Quotes[0].Code != "AAPL" {
			t.Errorf("expected only AAPL quote, got %+v", got.Quotes)
		}
		if len(got.Failures) != 1 {
			t.Fatalf("expected one failure, got %+v", got.Failures)
		}
		failure := got.Failures[0]
		if failure.Code != "ONE" || failure.Reason != candles.QuoteFailureInsufficientData || failure.Cause != nil {
			t.Errorf("unexpected failure: %+v", failure)
		}
	})

	t.Run("bars>0のときclosesは古い→新しい順で最大bars件返る", func(t *testing.T) {
		// 時刻降順（[0]が最新）: day5(50) > day4(40) > day3(30) > day2(20) > day1(10)
		all := []candles.Candle{mkCandle(5, 50), mkCandle(4, 40), mkCandle(3, 30), mkCandle(2, 20), mkCandle(1, 10)}
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				if outputsize != 3 {
					t.Errorf("unexpected outputsize: got %d, want 3 (bars=3)", outputsize)
				}
				return all[:outputsize], nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != 1 {
			t.Fatalf("expected 1 quote, got %d", len(got.Quotes))
		}

		want := []float64{30, 40, 50} // 古い→新しい順、直近3本
		if !reflect.DeepEqual(got.Quotes[0].Closes, want) {
			t.Errorf("unexpected closes: got %v, want %v", got.Quotes[0].Closes, want)
		}
	})

	t.Run("bars=0のときclosesはnil", func(t *testing.T) {
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return []candles.Candle{mkCandle(2, 105), mkCandle(1, 100)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Quotes[0].Closes != nil {
			t.Errorf("expected nil closes, got %v", got.Quotes[0].Closes)
		}
	})

	t.Run("prev_closeが0のときchange_percentは0", func(t *testing.T) {
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return []candles.Candle{mkCandle(2, 10), mkCandle(1, 0)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Quotes[0].ChangePercent != 0 {
			t.Errorf("expected change_percent 0 when prev_close is 0, got %v", got.Quotes[0].ChangePercent)
		}
		if got.Quotes[0].Change != 10 {
			t.Errorf("expected change 10, got %v", got.Quotes[0].Change)
		}
	})

	t.Run("partial success: リポジトリエラーを銘柄ごとの失敗として回収して他銘柄を返す", func(t *testing.T) {
		errDB := errors.New("database error")
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				if symbol == "BAD" {
					return nil, errDB
				}
				return []candles.Candle{mkCandle(2, 105), mkCandle(1, 100)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL", "BAD", "GOOGL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != 2 || got.Quotes[0].Code != "AAPL" || got.Quotes[1].Code != "GOOGL" {
			t.Errorf("unexpected quotes: %+v", got.Quotes)
		}
		if len(got.Failures) != 1 {
			t.Fatalf("expected one failure, got %+v", got.Failures)
		}
		failure := got.Failures[0]
		if failure.Code != "BAD" || failure.Reason != candles.QuoteFailureFetchFailed || !errors.Is(failure.Cause, errDB) {
			t.Errorf("unexpected failure: %+v", failure)
		}
	})

	t.Run("partial success: 全銘柄の取得に失敗しても銘柄ごとの結果を返す", func(t *testing.T) {
		errDB := errors.New("database error")
		codes := []string{"BAD1", "BAD2"}
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return nil, errDB
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, codes, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != 0 {
			t.Errorf("expected no quotes, got %+v", got.Quotes)
		}
		if len(got.Failures) != len(codes) {
			t.Fatalf("expected %d failures, got %+v", len(codes), got.Failures)
		}
		for i, code := range codes {
			failure := got.Failures[i]
			if failure.Code != code || failure.Reason != candles.QuoteFailureFetchFailed || !errors.Is(failure.Cause, errDB) {
				t.Errorf("failure[%d]=%+v", i, failure)
			}
		}
	})

	t.Run("success: 複数銘柄すべてが入力順で返る", func(t *testing.T) {
		codes := []string{"A", "B", "C", "D"}
		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return []candles.Candle{mkCandle(2, 20), mkCandle(1, 10)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, codes, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Quotes) != len(codes) {
			t.Fatalf("expected %d quotes, got %d", len(codes), len(got.Quotes))
		}
		if len(got.Failures) != 0 {
			t.Fatalf("expected no failures, got %+v", got.Failures)
		}
		for i, code := range codes {
			if got.Quotes[i].Code != code {
				t.Errorf("quote[%d].Code=%s, want %s", i, got.Quotes[i].Code, code)
			}
		}
	})

	t.Run("error: リクエストコンテキストの中断は全体エラーになる", func(t *testing.T) {
		requestCtx, cancel := context.WithCancel(context.Background())
		cancel()

		repo := &quoteMockRepository{
			FindFunc: func(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
				return nil, ctx.Err()
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(requestCtx, []string{"AAPL", "GOOGL"}, "1day", 0)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
		if len(got.Quotes) != 0 || len(got.Failures) != 0 {
			t.Errorf("expected empty result on request cancellation, got %+v", got)
		}
	})
}
