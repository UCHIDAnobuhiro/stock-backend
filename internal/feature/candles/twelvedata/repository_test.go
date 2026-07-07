package twelvedata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// retryTestConfig はリトライ系テストで使用する高速バックオフ設定の Config を返します。
func retryTestConfig(baseURL string, maxRetries int) Config {
	return Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          baseURL,
		MaxRetries:       maxRetries,
		RetryBaseBackoff: 1 * time.Millisecond,
		RetryMaxBackoff:  50 * time.Millisecond,
		RetryJitterRatio: 0.0,
	}
}

// TestNewTwelveDataMarket はTwelveDataMarketインスタンスが正しく生成されることを検証します。
func TestNewTwelveDataMarket(t *testing.T) {
	t.Parallel()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          "https://api.test.com",
		Timeout:          10 * time.Second,
	}
	client := &http.Client{}

	market := NewTwelveDataMarket(cfg, client)

	if market == nil {
		t.Fatal("expected non-nil market")
	}
	if market.cfg.TwelveDataAPIKey != cfg.TwelveDataAPIKey {
		t.Errorf("expected API key %q, got %q", cfg.TwelveDataAPIKey, market.cfg.TwelveDataAPIKey)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Success は正常なAPIレスポンスからローソク足データが正しくパースされることを検証します。
func TestTwelveDataMarket_GetTimeSeries_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request parameters
		if r.URL.Query().Get("symbol") != "AAPL" {
			t.Errorf("expected symbol AAPL, got %s", r.URL.Query().Get("symbol"))
		}
		if r.URL.Query().Get("interval") != "1day" {
			t.Errorf("expected interval 1day, got %s", r.URL.Query().Get("interval"))
		}
		if r.URL.Query().Get("outputsize") != "100" {
			t.Errorf("expected outputsize 100, got %s", r.URL.Query().Get("outputsize"))
		}
		if r.Header.Get("Authorization") != "apikey test-key" {
			t.Errorf("expected Authorization header 'apikey test-key', got %s", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("apikey") != "" {
			t.Errorf("expected apikey not present in query, got %s", r.URL.Query().Get("apikey"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"symbol": "AAPL",
			"interval": "1day",
			"values": [
				{
					"datetime": "2025-01-15",
					"open": "150.00",
					"high": "155.00",
					"low": "149.00",
					"close": "154.50",
					"volume": "1000000"
				},
				{
					"datetime": "2025-01-14 09:30:00",
					"open": "148.00",
					"high": "151.00",
					"low": "147.50",
					"close": "150.00",
					"volume": "900000"
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	candles, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(candles) != 2 {
		t.Fatalf("expected 2 candles, got %d", len(candles))
	}

	// Check first candle
	if candles[0].Open != 150.00 {
		t.Errorf("expected open 150.00, got %f", candles[0].Open)
	}
	if candles[0].Close != 154.50 {
		t.Errorf("expected close 154.50, got %f", candles[0].Close)
	}
	if candles[0].Volume != 1000000 {
		t.Errorf("expected volume 1000000, got %d", candles[0].Volume)
	}
}

// TestTwelveDataMarket_GetTimeSeries_ParseInLocation は loc を解釈ロケーションとして
// 取引所ローカル時刻の datetime が正しく時刻に変換されることを検証します。
// 米国株（DST 切替前後）と日本株（JST）の代表ケースを含みます。
func TestTwelveDataMarket_GetTimeSeries_ParseInLocation(t *testing.T) {
	t.Parallel()

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("failed to load NY tz: %v", err)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("failed to load Tokyo tz: %v", err)
	}

	tests := []struct {
		name     string
		datetime string
		loc      *time.Location
		wantUTC  time.Time
	}{
		{
			name:     "NY DST off (winter): EST is UTC-5",
			datetime: "2024-01-15 09:30:00",
			loc:      ny,
			wantUTC:  time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
		},
		{
			name:     "NY DST on (summer): EDT is UTC-4",
			datetime: "2024-07-15 09:30:00",
			loc:      ny,
			wantUTC:  time.Date(2024, 7, 15, 13, 30, 0, 0, time.UTC),
		},
		{
			name:     "Tokyo JST is UTC+9",
			datetime: "2024-04-01 00:00:00",
			loc:      tokyo,
			wantUTC:  time.Date(2024, 3, 31, 15, 0, 0, 0, time.UTC),
		},
		{
			name:     "date only (1day) interpreted at midnight in loc",
			datetime: "2024-04-01",
			loc:      tokyo,
			wantUTC:  time.Date(2024, 3, 31, 15, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				body := `{"status":"ok","values":[{"datetime":"` + tt.datetime +
					`","open":"1","high":"1","low":"1","close":"1","volume":"1"}]}`
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			cfg := Config{
				TwelveDataAPIKey: "test-key",
				BaseURL:          server.URL,
			}
			market := NewTwelveDataMarket(cfg, server.Client())

			candles, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 1, tt.loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(candles) != 1 {
				t.Fatalf("expected 1 candle, got %d", len(candles))
			}
			if !candles[0].Time.Equal(tt.wantUTC) {
				t.Errorf("expected UTC %v, got %v", tt.wantUTC, candles[0].Time.UTC())
			}
			if candles[0].Time.Location() != tt.loc {
				t.Errorf("expected location %v, got %v", tt.loc, candles[0].Time.Location())
			}
		})
	}
}

// TestTwelveDataMarket_GetTimeSeries_NilLocation は loc==nil の場合にエラーを返すことを検証します。
func TestTwelveDataMarket_GetTimeSeries_NilLocation(t *testing.T) {
	t.Parallel()

	cfg := Config{TwelveDataAPIKey: "test-key", BaseURL: "http://invalid"}
	market := NewTwelveDataMarket(cfg, &http.Client{})
	if _, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 1, nil); err == nil {
		t.Fatal("expected error for nil loc")
	}
}

// TestTwelveDataMarket_GetTimeSeries_HTTPError は各種HTTPエラーステータスコードが正しくエラーとして処理されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_HTTPError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
	}{
		{"bad request", http.StatusBadRequest},
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"not found", http.StatusNotFound},
		{"internal server error", http.StatusInternalServerError},
		{"service unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			cfg := Config{
				TwelveDataAPIKey: "test-key",
				BaseURL:          server.URL,
			}
			market := NewTwelveDataMarket(cfg, server.Client())

			_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "twelvedata http") {
				t.Errorf("expected HTTP error message, got %v", err)
			}
		})
	}
}

// TestTwelveDataMarket_GetTimeSeries_APIError はAPIレベルのエラーレスポンスが正しく処理されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_APIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "error",
			"message": "Invalid API key"
		}`))
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "invalid-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("expected API error message, got %v", err)
	}
}

// TestTwelveDataMarket_GetTimeSeries_InvalidJSON は不正なJSONレスポンスがエラーとして処理されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_InvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestTwelveDataMarket_GetTimeSeries_InvalidDateTime は不正な日時形式がエラーとして処理されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_InvalidDateTime(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"values": [
				{
					"datetime": "invalid-date",
					"open": "150.00",
					"high": "155.00",
					"low": "149.00",
					"close": "154.50",
					"volume": "1000000"
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse time") {
		t.Errorf("expected parse time error, got %v", err)
	}
}

// TestTwelveDataMarket_GetTimeSeries_InvalidNumbers は不正な数値データが各フィールドごとにエラーとして処理されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_InvalidNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		errField string
	}{
		{
			name: "invalid open",
			response: `{
				"status": "ok",
				"values": [{"datetime": "2025-01-15", "open": "abc", "high": "155.00", "low": "149.00", "close": "154.50", "volume": "1000000"}]
			}`,
			errField: "parse open",
		},
		{
			name: "invalid high",
			response: `{
				"status": "ok",
				"values": [{"datetime": "2025-01-15", "open": "150.00", "high": "xyz", "low": "149.00", "close": "154.50", "volume": "1000000"}]
			}`,
			errField: "parse high",
		},
		{
			name: "invalid low",
			response: `{
				"status": "ok",
				"values": [{"datetime": "2025-01-15", "open": "150.00", "high": "155.00", "low": "bad", "close": "154.50", "volume": "1000000"}]
			}`,
			errField: "parse low",
		},
		{
			name: "invalid close",
			response: `{
				"status": "ok",
				"values": [{"datetime": "2025-01-15", "open": "150.00", "high": "155.00", "low": "149.00", "close": "bad", "volume": "1000000"}]
			}`,
			errField: "parse close",
		},
		{
			name: "invalid volume",
			response: `{
				"status": "ok",
				"values": [{"datetime": "2025-01-15", "open": "150.00", "high": "155.00", "low": "149.00", "close": "154.50", "volume": "not-a-number"}]
			}`,
			errField: "parse volume",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			cfg := Config{
				TwelveDataAPIKey: "test-key",
				BaseURL:          server.URL,
			}
			market := NewTwelveDataMarket(cfg, server.Client())

			_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errField) {
				t.Errorf("expected error containing %q, got %v", tt.errField, err)
			}
		})
	}
}

// TestTwelveDataMarket_GetTimeSeries_EmptyValues は空のvalues配列で空のスライスが返されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_EmptyValues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"values": []
		}`))
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	candles, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 0 {
		t.Errorf("expected 0 candles, got %d", len(candles))
	}
}

// TestTwelveDataMarket_GetTimeSeries_ContextCancellation はコンテキストキャンセル時にエラーが返されることを検証します。
func TestTwelveDataMarket_GetTimeSeries_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          server.URL,
	}
	market := NewTwelveDataMarket(cfg, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := market.GetTimeSeries(ctx, "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}
}

// TestNewConfig はデフォルトのタイムアウト値とリトライ設定のデフォルト値が正しく設定されることを検証します。
func TestNewConfig(t *testing.T) {
	t.Parallel()

	cfg := NewConfig("api-key", "https://example.com")

	if cfg.TwelveDataAPIKey != "api-key" {
		t.Errorf("expected APIKey 'api-key', got %q", cfg.TwelveDataAPIKey)
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("expected BaseURL 'https://example.com', got %q", cfg.BaseURL)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("expected timeout 10s, got %v", cfg.Timeout)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", cfg.MaxRetries)
	}
	if cfg.RetryBaseBackoff != 500*time.Millisecond {
		t.Errorf("expected RetryBaseBackoff 500ms, got %v", cfg.RetryBaseBackoff)
	}
}

// successTimeSeriesBody は GetTimeSeries 成功レスポンスの最小 JSON です。
const successTimeSeriesBody = `{
	"status": "ok",
	"values": [
		{"datetime": "2025-01-15", "open": "150.00", "high": "155.00", "low": "149.00", "close": "154.50", "volume": "1000000"}
	]
}`

// TestTwelveDataMarket_GetTimeSeries_Retry_SuccessAfter503 は
// 503 が連続した後に成功レスポンスを返した場合、合計リクエスト回数とデータ取得成功を検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_SuccessAfter503(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successTimeSeriesBody))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(retryTestConfig(server.URL, 3), server.Client())

	candles, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("expected 1 candle, got %d", len(candles))
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 HTTP calls (2 retries + success), got %d", got)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_ExhaustedOn503 は
// 503 が継続する場合、MaxRetries+1 回試行後にエラーを返すことを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_ExhaustedOn503(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	market := NewTwelveDataMarket(retryTestConfig(server.URL, 3), server.Client())

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected error message to contain status code, got %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("expected 4 HTTP calls (1 initial + 3 retries), got %d", got)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_NoRetryOn4xx は
// 4xx（429 を除く）ではリトライせず即エラーを返すことを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_NoRetryOn4xx(t *testing.T) {
	t.Parallel()

	statuses := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity,
	}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
			}))
			defer server.Close()

			market := NewTwelveDataMarket(retryTestConfig(server.URL, 3), server.Client())

			_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("expected 1 HTTP call (no retry), got %d", got)
			}
		})
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_RespectsRetryAfter は
// 429 + Retry-After ヘッダで指定された秒数だけ待機してからリトライすることを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_RespectsRetryAfter(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successTimeSeriesBody))
	}))
	defer server.Close()

	cfg := retryTestConfig(server.URL, 3)
	// RetryMaxBackoff を Retry-After 以上にして、ヘッダ値が効くことを確認する。
	cfg.RetryMaxBackoff = 5 * time.Second
	market := NewTwelveDataMarket(cfg, server.Client())

	// Retry-After ヘッダ付きでもリトライが行われることを検証する。
	// Retry-After とバックオフの選択ロジック自体は TestComputeRetryDelay で別途検証する。
	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", got)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_RetryAfterHTTPDate は
// Retry-After に HTTP-date 形式が渡された場合も正しく扱われることを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_RetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// 過去日時 → 待機なしで即リトライされること
			w.Header().Set("Retry-After", time.Now().Add(-1*time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successTimeSeriesBody))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(retryTestConfig(server.URL, 3), server.Client())

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", got)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_NetworkError は
// ネットワークエラー（接続失敗）でリトライが行われ、最終的にエラーになることを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_NetworkError(t *testing.T) {
	t.Parallel()

	// サーバを起動して URL を取り、Close で接続を不可能にする
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	market := NewTwelveDataMarket(retryTestConfig(url, 2), &http.Client{Timeout: 200 * time.Millisecond})

	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_ContextCanceledMidRetry は
// バックオフ中に ctx がキャンセルされた場合、即座に終了することを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_ContextCanceledMidRetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := retryTestConfig(server.URL, 5)
	cfg.RetryBaseBackoff = 200 * time.Millisecond
	cfg.RetryMaxBackoff = 1 * time.Second
	market := NewTwelveDataMarket(cfg, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := market.GetTimeSeries(ctx, "AAPL", "1day", 100, time.UTC)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	// バックオフ全体（200ms+800ms+...）よりはるかに早く終了するはず
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected to return quickly on ctx cancel, took %v", elapsed)
	}
}

// TestParseRetryAfter は Retry-After ヘッダのパース挙動を検証します。
func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   time.Duration
		approx bool
	}{
		{"empty", "", 0, false},
		{"seconds", "5", 5 * time.Second, false},
		{"zero seconds", "0", 0, false},
		{"negative seconds", "-1", 0, false},
		{"invalid", "not-a-number", 0, false},
		{"overflow seconds", "99999999999", maxRetryAfterSecs * time.Second, false},
		{"upper bound seconds", "3600", maxRetryAfterSecs * time.Second, false},
		{"past http-date", time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat), 0, false},
		{"future http-date", time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat), 2 * time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := &http.Response{Header: http.Header{}}
			if tt.header != "" {
				res.Header.Set("Retry-After", tt.header)
			}
			got := parseRetryAfter(res)
			if tt.approx {
				if got <= 0 || got > 3*time.Second {
					t.Errorf("expected ~%v, got %v", tt.want, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

// TestIsRetryableStatus はリトライ対象ステータス判定を検証します。
func TestIsRetryableStatus(t *testing.T) {
	t.Parallel()

	retryable := []int{429, 500, 502, 503, 504, 599}
	notRetryable := []int{200, 301, 400, 401, 403, 404, 422, 418}

	for _, s := range retryable {
		if !isRetryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range notRetryable {
		if isRetryableStatus(s) {
			t.Errorf("status %d should not be retryable", s)
		}
	}
}

// TestComputeRetryDelay は待機時間決定ロジックを検証します（実待機なし）。
func TestComputeRetryDelay(t *testing.T) {
	t.Parallel()

	cfg := Config{
		RetryBaseBackoff: 500 * time.Millisecond,
		RetryMaxBackoff:  8 * time.Second,
		RetryJitterRatio: 0.0, // 決定的にするためジッター無効
	}

	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{"retry-after takes precedence", 0, 2 * time.Second, 2 * time.Second},
		{"retry-after clamped to max", 0, 30 * time.Second, 8 * time.Second},
		{"retry-after exactly at max", 0, 8 * time.Second, 8 * time.Second},
		{"backoff attempt 0", 0, 0, 500 * time.Millisecond},
		{"backoff attempt 1", 1, 0, 2 * time.Second},
		{"backoff attempt 2", 2, 0, 8 * time.Second},
		{"backoff attempt 3 clamped", 3, 0, 8 * time.Second},
		{"negative retry-after falls back to backoff", 0, -1 * time.Second, 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeRetryDelay(tt.attempt, tt.retryAfter, cfg)
			if got != tt.want {
				t.Errorf("computeRetryDelay(%d, %v) = %v, want %v", tt.attempt, tt.retryAfter, got, tt.want)
			}
		})
	}
}

// TestComputeRetryDelay_NoMaxBackoff は RetryMaxBackoff 未設定時に
// retryAfter がクランプされず素通しされることを検証します。
func TestComputeRetryDelay_NoMaxBackoff(t *testing.T) {
	t.Parallel()

	cfg := Config{
		RetryBaseBackoff: 500 * time.Millisecond,
		RetryMaxBackoff:  0, // クランプ無効
		RetryJitterRatio: 0.0,
	}

	got := computeRetryDelay(0, 1*time.Hour, cfg)
	if got != 1*time.Hour {
		t.Errorf("expected retry-after to pass through when MaxBackoff=0, got %v", got)
	}
}

// TestTwelveDataMarket_GetTimeSeries_Retry_NetworkError_NoSleepOnLastAttempt は
// ネットワークエラーが連続した場合、最終試行後に余分なバックオフ待機が発生しないことを検証します。
func TestTwelveDataMarket_GetTimeSeries_Retry_NetworkError_NoSleepOnLastAttempt(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	cfg := Config{
		TwelveDataAPIKey: "test-key",
		BaseURL:          url,
		MaxRetries:       2, // 合計 3 試行
		RetryBaseBackoff: 100 * time.Millisecond,
		RetryMaxBackoff:  500 * time.Millisecond,
		RetryJitterRatio: 0.0,
	}
	market := NewTwelveDataMarket(cfg, &http.Client{Timeout: 50 * time.Millisecond})

	start := time.Now()
	_, err := market.GetTimeSeries(context.Background(), "AAPL", "1day", 100, time.UTC)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	// 試行間バックオフは 100ms + 400ms = 500ms。最終試行後の追加待機があると elapsed が
	// さらに 1600ms 以上増える。最終試行後の待機が無いことを上限で確認する。
	// 各試行はクライアントタイムアウト 50ms 以下で失敗する想定。
	maxExpected := 100*time.Millisecond + 400*time.Millisecond + 3*50*time.Millisecond + 200*time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("expected total elapsed <= %v (no trailing sleep), got %v", maxExpected, elapsed)
	}
}
