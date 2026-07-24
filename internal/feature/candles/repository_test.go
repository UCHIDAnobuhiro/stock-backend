package candles

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := dbtest.OpenIsolatedDB(t)
	// candles は symbols.code への FK 制約があるため、テスト用に必要な銘柄をあらかじめ作成する。
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO symbols (code, name, market, timezone) VALUES
		   ('AAPL', 'Apple Inc.', 'NASDAQ', 'America/New_York'),
		   ('GOOGL', 'Alphabet Inc.', 'NASDAQ', 'America/New_York'),
		   ('NOTFOUND', 'Placeholder', 'TEST', 'UTC')
		 ON CONFLICT (code) DO NOTHING`)
	require.NoError(t, err)
	return db
}

// seedCandle はテスト用のローソク足データをデータベースに作成します。
func seedCandle(t *testing.T, db *sql.DB, symbol, interval string, ts time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO candles (symbol_code, "interval", "time", open, high, low, close, volume)
		 VALUES ($1, $2, $3, 100.0, 110.0, 90.0, 105.0, 1000)`,
		symbol, interval, ts,
	)
	require.NoError(t, err, "failed to seed candle")
}

// candleCount は candles テーブルの行数を返します。
func candleCount(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM candles`).Scan(&n))
	return n
}

func TestNewCandleRepository(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)
	assert.NotNil(t, repo)
	assert.NotNil(t, repo.db)
}

func TestCandleRepository_UpsertBatch(t *testing.T) {
	t.Parallel()
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		candles      []Candle
		setupFunc    func(t *testing.T, db *sql.DB)
		validateFunc func(t *testing.T, db *sql.DB)
	}{
		{
			name: "success: insert single candle",
			candles: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime, Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
			},
			validateFunc: func(t *testing.T, db *sql.DB) {
				assert.Equal(t, int64(1), candleCount(t, db))
			},
		},
		{
			name: "success: insert multiple candles",
			candles: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime, Open: 100, High: 110, Low: 90, Close: 105, Volume: 1000},
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime.AddDate(0, 0, 1), Open: 105, High: 115, Low: 95, Close: 110, Volume: 1500},
			},
			validateFunc: func(t *testing.T, db *sql.DB) {
				assert.Equal(t, int64(2), candleCount(t, db))
			},
		},
		{
			name:    "success: empty slice",
			candles: []Candle{},
			validateFunc: func(t *testing.T, db *sql.DB) {
				assert.Equal(t, int64(0), candleCount(t, db))
			},
		},
		{
			name: "success: upsert updates existing candle",
			candles: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime, Open: 200, High: 220, Low: 180, Close: 210, Volume: 2000},
			},
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
			},
			validateFunc: func(t *testing.T, db *sql.DB) {
				assert.Equal(t, int64(1), candleCount(t, db))
				var o, h, l, c float64
				var v int64
				require.NoError(t, db.QueryRowContext(context.Background(),
					`SELECT open, high, low, close, volume FROM candles LIMIT 1`).Scan(&o, &h, &l, &c, &v))
				assert.Equal(t, 200.0, o)
				assert.Equal(t, 220.0, h)
				assert.Equal(t, 180.0, l)
				assert.Equal(t, 210.0, c)
				assert.Equal(t, int64(2000), v)
			},
		},
		{
			name: "success: upsert with mixed insert and update",
			candles: []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime, Open: 200, High: 220, Low: 180, Close: 210, Volume: 2000},
				{SymbolCode: "AAPL", Interval: "1day", Time: baseTime.AddDate(0, 0, 1), Open: 210, High: 230, Low: 190, Close: 220, Volume: 2500},
			},
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
			},
			validateFunc: func(t *testing.T, db *sql.DB) {
				assert.Equal(t, int64(2), candleCount(t, db))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			require.NoError(t, repo.UpsertBatch(context.Background(), tt.candles))
			if tt.validateFunc != nil {
				tt.validateFunc(t, db)
			}
		})
	}
}

func TestCandleRepository_Find(t *testing.T) {
	t.Parallel()
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		symbol       string
		interval     string
		outputsize   int
		wantErr      error
		setupFunc    func(t *testing.T, db *sql.DB)
		validateFunc func(t *testing.T, candles []Candle)
	}{
		{
			name: "success: find candles by symbol and interval", symbol: "AAPL", interval: "1day", outputsize: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
				seedCandle(t, db, "AAPL", "1day", baseTime.AddDate(0, 0, 1))
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 2)
			},
		},
		{
			name: "success: empty result when no matching candles", symbol: "NOTFOUND", interval: "1day", outputsize: 10,
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Empty(t, candles)
			},
		},
		{
			name: "success: filter by symbol only", symbol: "AAPL", interval: "1day", outputsize: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
				seedCandle(t, db, "GOOGL", "1day", baseTime)
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 1)
				assert.Equal(t, "AAPL", candles[0].SymbolCode)
			},
		},
		{
			name: "success: filter by interval", symbol: "AAPL", interval: "1day", outputsize: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
				seedCandle(t, db, "AAPL", "1week", baseTime)
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 1)
				assert.Equal(t, "1day", candles[0].Interval)
			},
		},
		{
			name: "success: respect outputsize limit", symbol: "AAPL", interval: "1day", outputsize: 2,
			setupFunc: func(t *testing.T, db *sql.DB) {
				for i := 0; i < 5; i++ {
					seedCandle(t, db, "AAPL", "1day", baseTime.AddDate(0, 0, i))
				}
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 2)
			},
		},
		{
			name: "success: results ordered by time descending", symbol: "AAPL", interval: "1day", outputsize: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
				seedCandle(t, db, "AAPL", "1day", baseTime.AddDate(0, 0, 2))
				seedCandle(t, db, "AAPL", "1day", baseTime.AddDate(0, 0, 1))
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 3)
				assert.True(t, candles[0].Time.After(candles[1].Time))
				assert.True(t, candles[1].Time.After(candles[2].Time))
			},
		},
		{
			name: "error: outputsize zero returns ErrInvalidOutputSize", symbol: "AAPL", interval: "1day", outputsize: 0,
			wantErr: ErrInvalidOutputSize,
		},
		{
			name: "error: negative outputsize returns ErrInvalidOutputSize", symbol: "AAPL", interval: "1day", outputsize: -1,
			wantErr: ErrInvalidOutputSize,
		},
		{
			name: "error: outputsize exceeding MaxOutputSize returns ErrInvalidOutputSize", symbol: "AAPL", interval: "1day", outputsize: MaxOutputSize + 1,
			wantErr: ErrInvalidOutputSize,
		},
		{
			name: "success: outputsize equal to MaxOutputSize is allowed", symbol: "AAPL", interval: "1day", outputsize: MaxOutputSize,
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Empty(t, candles)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			candles, err := repo.Find(context.Background(), tt.symbol, tt.interval, tt.outputsize)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.validateFunc != nil {
				tt.validateFunc(t, candles)
			}
		})
	}
}

func TestCandleRepository_FindLatestBySymbols(t *testing.T) {
	t.Parallel()
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		codes        []string
		interval     string
		n            int
		wantErr      error
		setupFunc    func(t *testing.T, db *sql.DB)
		validateFunc func(t *testing.T, candles []Candle)
	}{
		{
			name:  "success: 複数銘柄で各銘柄n件・時刻降順に取得できる",
			codes: []string{"AAPL", "GOOGL"}, interval: "1day", n: 2,
			setupFunc: func(t *testing.T, db *sql.DB) {
				for i := 0; i < 3; i++ {
					seedCandle(t, db, "AAPL", "1day", baseTime.AddDate(0, 0, i))
					seedCandle(t, db, "GOOGL", "1day", baseTime.AddDate(0, 0, i))
				}
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				byCode := map[string][]Candle{}
				for _, c := range candles {
					byCode[c.SymbolCode] = append(byCode[c.SymbolCode], c)
				}
				assert.Len(t, byCode["AAPL"], 2)
				assert.Len(t, byCode["GOOGL"], 2)
				// 各銘柄内で時刻降順になっていること
				assert.True(t, byCode["AAPL"][0].Time.After(byCode["AAPL"][1].Time))
				assert.True(t, byCode["GOOGL"][0].Time.After(byCode["GOOGL"][1].Time))
			},
		},
		{
			name:  "success: intervalフィルタが効く",
			codes: []string{"AAPL"}, interval: "1day", n: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
				seedCandle(t, db, "AAPL", "1week", baseTime)
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 1)
				assert.Equal(t, "1day", candles[0].Interval)
			},
		},
		{
			name:  "success: 存在しない銘柄コードの行は返らない",
			codes: []string{"AAPL", "NOTFOUND"}, interval: "1day", n: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Len(t, candles, 1)
				assert.Equal(t, "AAPL", candles[0].SymbolCode)
			},
		},
		{
			name:  "success: codesが空スライスなら空結果でクエリ発行なし",
			codes: []string{}, interval: "1day", n: 10,
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedCandle(t, db, "AAPL", "1day", baseTime)
			},
			validateFunc: func(t *testing.T, candles []Candle) {
				assert.Empty(t, candles)
			},
		},
		{
			name:  "error: n=0はErrInvalidOutputSize",
			codes: []string{"AAPL"}, interval: "1day", n: 0,
			wantErr: ErrInvalidOutputSize,
		},
		{
			name:  "error: n=MaxOutputSize+1はErrInvalidOutputSize",
			codes: []string{"AAPL"}, interval: "1day", n: MaxOutputSize + 1,
			wantErr: ErrInvalidOutputSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			candles, err := repo.FindLatestBySymbols(context.Background(), tt.codes, tt.interval, tt.n)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.validateFunc != nil {
				tt.validateFunc(t, candles)
			}
		})
	}
}

func TestCandleRepository_Find_EntityMapping(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)

	testTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO candles (symbol_code, "interval", "time", open, high, low, close, volume)
		 VALUES ('AAPL', '1day', $1, 150.5, 155.75, 149.25, 154.0, 5000000)`, testTime)
	require.NoError(t, err)

	result, err := repo.Find(context.Background(), "AAPL", "1day", 1)
	require.NoError(t, err)
	require.Len(t, result, 1)

	assert.Equal(t, "AAPL", result[0].SymbolCode)
	assert.Equal(t, "1day", result[0].Interval)
	assert.Equal(t, testTime.Unix(), result[0].Time.Unix())
	assert.Equal(t, 150.5, result[0].Open)
	assert.Equal(t, 155.75, result[0].High)
	assert.Equal(t, 149.25, result[0].Low)
	assert.Equal(t, 154.0, result[0].Close)
	assert.Equal(t, int64(5000000), result[0].Volume)
}
