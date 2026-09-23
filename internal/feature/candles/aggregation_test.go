//go:build !integration

package candles

import (
	"testing"
	"time"
)

func mustDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestAggregateWeekly(t *testing.T) {
	testCases := []struct {
		name     string
		input    []Candle
		expected []Candle
	}{
		{
			name:     "empty input returns nil",
			input:    []Candle{},
			expected: nil,
		},
		{
			name: "single candle: correct week start and OHLCV",
			// 2023-06-15 は木曜日 → 週開始は 2023-06-12（月曜）
			input: []Candle{
				{Time: mustDate(2023, 6, 15), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
			expected: []Candle{
				{Time: mustDate(2023, 6, 12), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
		},
		{
			name: "Sunday maps to preceding Monday",
			// 2023-06-18 は日曜 → ISO week 開始は 2023-06-12（月曜）
			input: []Candle{
				{Time: mustDate(2023, 6, 18), Open: 200, High: 220, Low: 190, Close: 210, Volume: 500},
			},
			expected: []Candle{
				{Time: mustDate(2023, 6, 12), Open: 200, High: 220, Low: 190, Close: 210, Volume: 500},
			},
		},
		{
			name: "multiple candles in same week aggregate correctly",
			// 2023-06-12（月）〜 2023-06-14（水）は同一週
			input: []Candle{
				{Time: mustDate(2023, 6, 12), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
				{Time: mustDate(2023, 6, 13), Open: 105, High: 115, Low: 95, Close: 112, Volume: 1200},
				{Time: mustDate(2023, 6, 14), Open: 112, High: 108, Low: 88, Close: 100, Volume: 900},
			},
			expected: []Candle{
				{
					Time:   mustDate(2023, 6, 12),
					Open:   100,  // 最初の日
					High:   115,  // 最大
					Low:    88,   // 最小
					Close:  100,  // 最後の日
					Volume: 3100, // 合計
				},
			},
		},
		{
			name: "candles spanning two weeks produce two results in chronological order",
			// 2023-06-09（金）: ISO W23 / 2023-06-12（月）: ISO W24
			input: []Candle{
				{Time: mustDate(2023, 6, 12), Open: 200, High: 210, Low: 195, Close: 205, Volume: 2000},
				{Time: mustDate(2023, 6, 9), Open: 100, High: 115, Low: 95, Close: 110, Volume: 1000},
			},
			expected: []Candle{
				{Time: mustDate(2023, 6, 5), Open: 100, High: 115, Low: 95, Close: 110, Volume: 1000},   // W23 開始
				{Time: mustDate(2023, 6, 12), Open: 200, High: 210, Low: 195, Close: 205, Volume: 2000}, // W24 開始
			},
		},
		{
			name: "newest-first input still produces correct aggregation",
			// 入力が最新順（APIのデフォルト）でも正しく集計される
			input: []Candle{
				{Time: mustDate(2023, 6, 14), Open: 112, High: 108, Low: 88, Close: 100, Volume: 900},
				{Time: mustDate(2023, 6, 13), Open: 105, High: 115, Low: 95, Close: 112, Volume: 1200},
				{Time: mustDate(2023, 6, 12), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
			expected: []Candle{
				{
					Time:   mustDate(2023, 6, 12),
					Open:   100, // 昇順で最初の日（2023-06-12）の Open
					High:   115,
					Low:    88,
					Close:  100, // 昇順で最後の日（2023-06-14）の Close
					Volume: 3100,
				},
			},
		},
		{
			name: "year-boundary ISO week: 2023-01-01 is in ISO week 2022-W52",
			// 2023-01-01（日）と 2022-12-31（土）は ISO 2022-W52
			input: []Candle{
				{Time: mustDate(2023, 1, 1), Open: 100, High: 110, Low: 90, Close: 105, Volume: 500},
				{Time: mustDate(2022, 12, 31), Open: 95, High: 105, Low: 85, Close: 100, Volume: 600},
			},
			expected: []Candle{
				{
					Time:   mustDate(2022, 12, 26), // ISO 2022-W52 の月曜
					Open:   95,
					High:   110,
					Low:    85,
					Close:  105,
					Volume: 1100,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateWeekly(tc.input, time.UTC)
			assertCandlesEqual(t, got, tc.expected)
		})
	}
}

func TestAggregateMonthly(t *testing.T) {
	testCases := []struct {
		name     string
		input    []Candle
		expected []Candle
	}{
		{
			name:     "empty input returns nil",
			input:    []Candle{},
			expected: nil,
		},
		{
			name: "single candle: correct month start and OHLCV",
			input: []Candle{
				{Time: mustDate(2023, 6, 15), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
			expected: []Candle{
				{Time: mustDate(2023, 6, 1), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
		},
		{
			name: "multiple candles in same month aggregate correctly",
			input: []Candle{
				{Time: mustDate(2023, 6, 1), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
				{Time: mustDate(2023, 6, 15), Open: 105, High: 120, Low: 95, Close: 115, Volume: 2000},
				{Time: mustDate(2023, 6, 30), Open: 115, High: 112, Low: 88, Close: 110, Volume: 1500},
			},
			expected: []Candle{
				{
					Time:   mustDate(2023, 6, 1),
					Open:   100,
					High:   120,
					Low:    88,
					Close:  110,
					Volume: 4500,
				},
			},
		},
		{
			name: "candles spanning two months produce two results in chronological order",
			input: []Candle{
				{Time: mustDate(2023, 7, 1), Open: 200, High: 210, Low: 195, Close: 205, Volume: 2000},
				{Time: mustDate(2023, 6, 30), Open: 100, High: 115, Low: 95, Close: 110, Volume: 1000},
			},
			expected: []Candle{
				{Time: mustDate(2023, 6, 1), Open: 100, High: 115, Low: 95, Close: 110, Volume: 1000},
				{Time: mustDate(2023, 7, 1), Open: 200, High: 210, Low: 195, Close: 205, Volume: 2000},
			},
		},
		{
			name: "year boundary: December and January produce separate monthly candles",
			input: []Candle{
				{Time: mustDate(2023, 1, 1), Open: 100, High: 110, Low: 90, Close: 105, Volume: 500},
				{Time: mustDate(2022, 12, 31), Open: 95, High: 105, Low: 85, Close: 100, Volume: 600},
			},
			expected: []Candle{
				{Time: mustDate(2022, 12, 1), Open: 95, High: 105, Low: 85, Close: 100, Volume: 600},
				{Time: mustDate(2023, 1, 1), Open: 100, High: 110, Low: 90, Close: 105, Volume: 500},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateMonthly(tc.input, time.UTC)
			assertCandlesEqual(t, got, tc.expected)
		})
	}
}

// TestAggregateMonthly_LocaleBoundary は loc に取引所ローカル TZ を渡した場合に
// 月境界判定が UTC ではなくロケーションで行われることを検証します。
// 米国株: 2024-12-31 21:00 ET (UTC では 2025-01-01 02:00) は ET 12月、2025-01-01 09:30 ET は ET 1月。
func TestAggregateMonthly_LocaleBoundary(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load NY tz: %v", err)
	}
	dec31ET := time.Date(2024, 12, 31, 21, 0, 0, 0, ny)
	jan1ET := time.Date(2025, 1, 1, 9, 30, 0, 0, ny)

	input := []Candle{
		{Time: dec31ET, Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
		{Time: jan1ET, Open: 105, High: 115, Low: 100, Close: 112, Volume: 2000},
	}

	gotLoc := aggregateMonthly(input, ny)
	if len(gotLoc) != 2 {
		t.Fatalf("loc=ET: expected 2 monthly buckets, got %d", len(gotLoc))
	}
	if !gotLoc[0].Time.Equal(time.Date(2024, 12, 1, 0, 0, 0, 0, ny)) {
		t.Errorf("loc=ET first bucket: got %v, want 2024-12-01 ET", gotLoc[0].Time)
	}
	if !gotLoc[1].Time.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, ny)) {
		t.Errorf("loc=ET second bucket: got %v, want 2025-01-01 ET", gotLoc[1].Time)
	}

	// loc=UTC で集計した場合は dec31ET (UTC では 2025-01-01) と jan1ET (UTC では 2025-01-01) が
	// 同じ 2025-01 月バケットに合算される。これは TZ を考慮しない既存挙動と一致する。
	gotUTC := aggregateMonthly(input, time.UTC)
	if len(gotUTC) != 1 {
		t.Fatalf("loc=UTC: expected 1 monthly bucket (incorrect aggregation when ignoring TZ), got %d", len(gotUTC))
	}
}

// TestAggregateWeekly_LocaleBoundary は loc に取引所ローカル TZ を渡した場合に
// 週境界判定がロケーションで行われることを検証します。
func TestAggregateWeekly_LocaleBoundary(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load NY tz: %v", err)
	}
	// 2024-03-10 は DST 切替日（米国）。9:30 ET は EDT (UTC-4)。
	mon := time.Date(2024, 3, 11, 9, 30, 0, 0, ny) // 月曜
	wed := time.Date(2024, 3, 13, 9, 30, 0, 0, ny) // 水曜（同週）

	input := []Candle{
		{Time: mon, Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
		{Time: wed, Open: 106, High: 120, Low: 95, Close: 115, Volume: 1500},
	}

	got := aggregateWeekly(input, ny)
	if len(got) != 1 {
		t.Fatalf("expected 1 weekly bucket, got %d", len(got))
	}
	want := time.Date(2024, 3, 11, 0, 0, 0, 0, ny)
	if !got[0].Time.Equal(want) {
		t.Errorf("week start: got %v, want %v", got[0].Time, want)
	}
}

// assertCandlesEqual は2つの Candle スライスを比較します（SymbolCode/Interval は無視）。
func assertCandlesEqual(t *testing.T, got, want []Candle) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
		return
	}
	if len(got) != len(want) {
		t.Fatalf("candle count: got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if !got[i].Time.Equal(want[i].Time) {
			t.Errorf("[%d] Time: got %v, want %v", i, got[i].Time, want[i].Time)
		}
		if got[i].Open != want[i].Open {
			t.Errorf("[%d] Open: got %v, want %v", i, got[i].Open, want[i].Open)
		}
		if got[i].High != want[i].High {
			t.Errorf("[%d] High: got %v, want %v", i, got[i].High, want[i].High)
		}
		if got[i].Low != want[i].Low {
			t.Errorf("[%d] Low: got %v, want %v", i, got[i].Low, want[i].Low)
		}
		if got[i].Close != want[i].Close {
			t.Errorf("[%d] Close: got %v, want %v", i, got[i].Close, want[i].Close)
		}
		if got[i].Volume != want[i].Volume {
			t.Errorf("[%d] Volume: got %v, want %v", i, got[i].Volume, want[i].Volume)
		}
	}
}
