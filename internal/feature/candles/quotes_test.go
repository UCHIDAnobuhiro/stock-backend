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
// GetQuotesはFindLatestBySymbolsを1回だけ呼び出すため、FindLatestBySymbolsFuncで
// フラットな結果（銘柄ごとに時刻降順で連結）を返せるようにします。
type quoteMockRepository struct {
	FindLatestBySymbolsFunc  func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error)
	FindLatestBySymbolsCalls int
}

// Find はRepositoryインターフェースを満たすためだけに存在します。
// GetQuotesから呼ばれることはないため、呼ばれた場合はテスト失敗として扱います。
func (m *quoteMockRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error) {
	return nil, errors.New("Find should not be called by GetQuotes")
}

// FindLatestBySymbols はFindLatestBySymbolsFuncに処理を委譲し、呼び出し回数を記録します。
func (m *quoteMockRepository) FindLatestBySymbols(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
	m.FindLatestBySymbolsCalls++
	return m.FindLatestBySymbolsFunc(ctx, codes, interval, n)
}

// flattenByCode は byCode（銘柄→時刻降順のCandleスライス）を codes の順に連結した
// フラットな []Candle に変換するテストヘルパーです（FindLatestBySymbols の戻り値を模す）。
func flattenByCode(codes []string, byCode map[string][]candles.Candle) []candles.Candle {
	var flat []candles.Candle
	for _, code := range codes {
		flat = append(flat, byCode[code]...)
	}
	return flat
}

// mkCandle は指定銘柄コード・2024年1月dayの日付・終値closeを持つCandleを生成する
// テストヘルパーです。GetQuotesはSymbolCodeで銘柄ごとにグルーピングするため、
// SymbolCodeを必ず設定します。
func mkCandle(symbol string, day int, close float64) candles.Candle {
	return candles.Candle{
		SymbolCode: symbol,
		Time:       time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC),
		Close:      close,
	}
}

// TestCandlesUsecase_GetQuotes はGetQuotesメソッドの変換ロジック・FindLatestBySymbolsの
// 呼び出し方をテストします。
func TestCandlesUsecase_GetQuotes(t *testing.T) {
	ctx := context.Background()

	t.Run("success: change and change_percent are computed from the two most recent candles", func(t *testing.T) {
		byCode := map[string][]candles.Candle{
			// FindLatestBySymbolsは銘柄ごとに時刻降順（[0]が最新）で返す。
			"AAPL":  {mkCandle("AAPL", 2, 105), mkCandle("AAPL", 1, 100)},
			"GOOGL": {mkCandle("GOOGL", 2, 210), mkCandle("GOOGL", 1, 200)},
		}
		codes := []string{"AAPL", "GOOGL"}
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, gotCodes []string, interval string, n int) ([]candles.Candle, error) {
				if interval != "1day" {
					t.Errorf("unexpected interval: got %s, want 1day", interval)
				}
				// bars=0でも前日比計算に2本必要なため n は max(bars, 2) = 2 になる。
				if n != 2 {
					t.Errorf("unexpected n: got %d, want 2", n)
				}
				if !reflect.DeepEqual(gotCodes, codes) {
					t.Errorf("unexpected codes: got %v, want %v", gotCodes, codes)
				}
				return flattenByCode(codes, byCode), nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, codes, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 quotes, got %d", len(got))
		}
		if repo.FindLatestBySymbolsCalls != 1 {
			t.Errorf("expected FindLatestBySymbols to be called once, got %d", repo.FindLatestBySymbolsCalls)
		}

		byResultCode := make(map[string]candles.Quote, len(got))
		for _, q := range got {
			byResultCode[q.Code] = q
		}

		aaplChange := 105.0 - 100.0
		aaplWantChangePercent := aaplChange / 100.0 * 100
		aapl, ok := byResultCode["AAPL"]
		if !ok {
			t.Fatalf("AAPL not found in result: %+v", got)
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
			t.Fatalf("GOOGL not found in result: %+v", got)
		}
		if googl.Close != 210 || googl.PrevClose != 200 || googl.Change != googlChange || googl.ChangePercent != googlWantChangePercent {
			t.Errorf("unexpected GOOGL quote: %+v", googl)
		}
	})

	t.Run("2本未満のローソク足しかない銘柄は結果から除外される", func(t *testing.T) {
		byCode := map[string][]candles.Candle{
			"AAPL": {mkCandle("AAPL", 2, 105), mkCandle("AAPL", 1, 100)},
			"ONE":  {mkCandle("ONE", 1, 100)},
		}
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
				return flattenByCode(codes, byCode), nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL", "ONE"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Code != "AAPL" {
			t.Errorf("expected only AAPL to be included, got %+v", got)
		}
	})

	t.Run("bars>0のときclosesは古い→新しい順で最大bars件返る", func(t *testing.T) {
		// 時刻降順（[0]が最新）: day5(50) > day4(40) > day3(30) > day2(20) > day1(10)
		all := []candles.Candle{
			mkCandle("AAPL", 5, 50), mkCandle("AAPL", 4, 40), mkCandle("AAPL", 3, 30),
			mkCandle("AAPL", 2, 20), mkCandle("AAPL", 1, 10),
		}
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
				if n != 3 {
					t.Errorf("unexpected n: got %d, want 3 (bars=3)", n)
				}
				return all[:n], nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 quote, got %d", len(got))
		}

		want := []float64{30, 40, 50} // 古い→新しい順、直近3本
		if !reflect.DeepEqual(got[0].Closes, want) {
			t.Errorf("unexpected closes: got %v, want %v", got[0].Closes, want)
		}
	})

	t.Run("bars=0のときclosesはnil", func(t *testing.T) {
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
				return []candles.Candle{mkCandle("AAPL", 2, 105), mkCandle("AAPL", 1, 100)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Closes != nil {
			t.Errorf("expected nil closes, got %v", got[0].Closes)
		}
	})

	t.Run("prev_closeが0のときchange_percentは0", func(t *testing.T) {
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
				return []candles.Candle{mkCandle("AAPL", 2, 10), mkCandle("AAPL", 1, 0)}, nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL"}, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].ChangePercent != 0 {
			t.Errorf("expected change_percent 0 when prev_close is 0, got %v", got[0].ChangePercent)
		}
		if got[0].Change != 10 {
			t.Errorf("expected change 10, got %v", got[0].Change)
		}
	})

	t.Run("error: FindLatestBySymbolsがエラーを返した場合は全体がエラーになる", func(t *testing.T) {
		errDB := errors.New("database error")
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, codes []string, interval string, n int) ([]candles.Candle, error) {
				return nil, errDB
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, []string{"AAPL", "BAD", "GOOGL"}, "1day", 0)
		if !errors.Is(err, errDB) {
			t.Fatalf("expected error %v, got %v", errDB, err)
		}
		if got != nil {
			t.Errorf("expected nil result on error, got %v", got)
		}
	})

	t.Run("success: 複数銘柄すべてがcodesの順で返る", func(t *testing.T) {
		codes := []string{"A", "B", "C", "D"}
		byCode := make(map[string][]candles.Candle, len(codes))
		for _, c := range codes {
			byCode[c] = []candles.Candle{mkCandle(c, 2, 20), mkCandle(c, 1, 10)}
		}
		repo := &quoteMockRepository{
			FindLatestBySymbolsFunc: func(ctx context.Context, gotCodes []string, interval string, n int) ([]candles.Candle, error) {
				return flattenByCode(gotCodes, byCode), nil
			},
		}
		uc := candles.NewUsecase(repo)

		got, err := uc.GetQuotes(ctx, codes, "1day", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(codes) {
			t.Fatalf("expected %d quotes, got %d", len(codes), len(got))
		}

		gotCodes := make([]string, len(got))
		for i, q := range got {
			gotCodes[i] = q.Code
		}
		if !reflect.DeepEqual(gotCodes, codes) {
			t.Errorf("expected result order %v, got %v", codes, gotCodes)
		}
		if repo.FindLatestBySymbolsCalls != 1 {
			t.Errorf("expected FindLatestBySymbols to be called once, got %d", repo.FindLatestBySymbolsCalls)
		}
	})
}
