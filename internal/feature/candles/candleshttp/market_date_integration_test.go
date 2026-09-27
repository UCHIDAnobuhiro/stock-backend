//go:build integration

package candleshttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

func TestIntegrationMarketDateAcrossDatabaseAndCache(t *testing.T) {
	tests := []struct {
		name, symbol, timezone, interval string
		year                             int
		month                            time.Month
		day                              int
	}{
		{"Tokyo day at year boundary", "7203.T", "Asia/Tokyo", "1day", 2024, time.January, 1},
		{"Tokyo week at year boundary", "7203.T", "Asia/Tokyo", "1week", 2024, time.January, 1},
		{"Tokyo month at year boundary", "7203.T", "Asia/Tokyo", "1month", 2024, time.January, 1},
		{"Tokyo ordinary day", "7203.T", "Asia/Tokyo", "1day", 2026, time.September, 25},
		{"New York winter day", "AAPL", "America/New_York", "1day", 2024, time.January, 1},
		{"New York winter week", "AAPL", "America/New_York", "1week", 2024, time.January, 1},
		{"New York winter month", "AAPL", "America/New_York", "1month", 2024, time.January, 1},
		{"New York summer day", "AAPL", "America/New_York", "1day", 2024, time.July, 1},
		{"New York summer week", "AAPL", "America/New_York", "1week", 2024, time.July, 1},
		{"New York summer month", "AAPL", "America/New_York", "1month", 2024, time.July, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbtest.OpenIsolatedDB(t)
			ctx := t.Context()
			_, err := db.ExecContext(ctx, `INSERT INTO symbols (code, name, market, timezone) VALUES ($1, 'Test', 'TEST', $2)`, tt.symbol, tt.timezone)
			require.NoError(t, err)
			loc, err := time.LoadLocation(tt.timezone)
			require.NoError(t, err)
			latest := time.Date(tt.year, tt.month, tt.day, 0, 0, 0, 0, loc)
			previous := latest.AddDate(0, 0, -1)
			switch tt.interval {
			case "1week":
				previous = latest.AddDate(0, 0, -7)
			case "1month":
				previous = latest.AddDate(0, -1, 0)
			}
			for i, ts := range []time.Time{previous, latest} {
				_, err = db.ExecContext(ctx, `INSERT INTO candles (symbol_code, "interval", "time", open, high, low, close, volume) VALUES ($1, $2, $3, 1, 1, 1, $4, 1)`, tt.symbol, tt.interval, ts, i+1)
				require.NoError(t, err)
			}

			direct := candles.NewRepository(db)
			rows, err := direct.Find(ctx, tt.symbol, tt.interval, 2)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			require.True(t, rows[0].Time.Equal(latest), "database timestamp changed: %s vs %s", rows[0].Time, latest)
			require.Equal(t, tt.timezone, rows[0].Timezone)
			require.Equal(t, latest.Format("2006-01-02"), rows[0].Time.Format("2006-01-02"))

			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			cached := candles.NewCachingRepository(rdb, time.Minute, direct, "candles")
			usecase := candles.NewUsecase(cached)
			handler := candleshttp.NewHandler(usecase)
			router := chi.NewRouter()
			router.Get("/v1/candles/{code}", handler.GetCandlesHandler)
			router.Get("/v1/quotes", handler.GetQuotesHandler)
			directHandler := candleshttp.NewHandler(candles.NewUsecase(direct))
			directRouter := chi.NewRouter()
			directRouter.Get("/v1/candles/{code}", directHandler.GetCandlesHandler)
			directRouter.Get("/v1/quotes", directHandler.GetQuotesHandler)

			checkResponses := func(stage string, route http.Handler, forceQuoteMiss bool) [2]string {
				t.Helper()
				url := fmt.Sprintf("/v1/candles/%s?interval=%s&outputsize=2", tt.symbol, tt.interval)
				response := performMarketDateRequest(t, route, url)
				candleJSON := string(response)
				var candleBody api.CandlesResponse
				require.NoError(t, json.Unmarshal(response, &candleBody))
				require.Len(t, candleBody.Candles, 2, stage)
				require.Equal(t, latest.Format("2006-01-02"), candleBody.Candles[0].Time, stage)

				if forceQuoteMiss {
					require.NoError(t, rdb.Del(ctx, fmt.Sprintf("candles:%s:%s", tt.symbol, tt.interval)).Err())
				}
				url = fmt.Sprintf("/v1/quotes?codes=%s&interval=%s&bars=2", tt.symbol, tt.interval)
				response = performMarketDateRequest(t, route, url)
				quoteJSON := string(response)
				var quoteBody api.QuoteBatchResponse
				require.NoError(t, json.Unmarshal(response, &quoteBody))
				require.Empty(t, quoteBody.Failures, stage)
				require.Len(t, quoteBody.Quotes, 1, stage)
				require.Equal(t, latest.Format("2006-01-02"), quoteBody.Quotes[0].Time, stage)
				return [2]string{candleJSON, quoteJSON}
			}
			directResponses := checkResponses("direct DB", directRouter, false)
			missResponses := checkResponses("cache miss", router, true)
			for i := range directResponses {
				require.JSONEq(t, directResponses[i], missResponses[i])
			}
			cachedRows, err := cached.Find(ctx, tt.symbol, tt.interval, 2)
			require.NoError(t, err)
			require.True(t, cachedRows[0].Time.Equal(latest))
			require.Equal(t, tt.timezone, cachedRows[0].Time.Location().String())

			// DBを空にしても、次の両APIが同じ日付を返せればRedisヒットが実証できる。
			_, err = db.ExecContext(ctx, `DELETE FROM candles WHERE symbol_code = $1 AND "interval" = $2`, tt.symbol, tt.interval)
			require.NoError(t, err)
			hitResponses := checkResponses("cache hit", router, false)
			for i := range directResponses {
				require.JSONEq(t, directResponses[i], hitResponses[i])
			}
		})
	}
}

func performMarketDateRequest(t *testing.T, handler http.Handler, url string) []byte {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	return recorder.Body.Bytes()
}
