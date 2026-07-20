package candles

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
)

// mockReadWriteRepository はテスト用の readWriteRepository（読み書き）モック実装です。
type mockReadWriteRepository struct {
	findFn        func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error)
	upsertBatchFn func(ctx context.Context, candles []Candle) error
}

// Find はモックのFind関数を呼び出します。
func (m *mockReadWriteRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
	if m.findFn != nil {
		return m.findFn(ctx, symbol, interval, outputsize)
	}
	return nil, nil
}

// UpsertBatch はモックのUpsertBatch関数を呼び出します。
func (m *mockReadWriteRepository) UpsertBatch(ctx context.Context, candles []Candle) error {
	if m.upsertBatchFn != nil {
		return m.upsertBatchFn(ctx, candles)
	}
	return nil
}

// TestNewCachingCandleRepository_Defaults はデフォルト値（TTLとnamespace）が正しく設定されることを検証します。
func TestNewCachingCandleRepository_Defaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		ttl               time.Duration
		namespace         string
		expectedTTL       time.Duration
		expectedNamespace string
	}{
		{
			name:              "default values when zero/empty",
			ttl:               0,
			namespace:         "",
			expectedTTL:       5 * time.Minute,
			expectedNamespace: "candles",
		},
		{
			name:              "negative ttl uses default",
			ttl:               -1 * time.Minute,
			namespace:         "",
			expectedTTL:       5 * time.Minute,
			expectedNamespace: "candles",
		},
		{
			name:              "custom values preserved",
			ttl:               10 * time.Minute,
			namespace:         "custom",
			expectedTTL:       10 * time.Minute,
			expectedNamespace: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := NewCachingRepository(nil, tt.ttl, &mockReadWriteRepository{}, tt.namespace)

			if repo.ttl != tt.expectedTTL {
				t.Errorf("expected TTL %v, got %v", tt.expectedTTL, repo.ttl)
			}
			if repo.namespace != tt.expectedNamespace {
				t.Errorf("expected namespace %q, got %q", tt.expectedNamespace, repo.namespace)
			}
		})
	}
}

// TestCachingCandleRepository_Find_NilRedis はRedisがnilの場合にキャッシュをバイパスして内部リポジトリを直接呼び出すことを検証します。
func TestCachingCandleRepository_Find_NilRedis(t *testing.T) {
	t.Parallel()

	expectedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return expectedCandles, nil
		},
	}

	// Redis is nil - should bypass cache and call inner directly
	repo := NewCachingRepository(nil, 5*time.Minute, inner, "candles")

	candles, err := repo.Find(context.Background(), "AAPL", "1day", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != len(expectedCandles) {
		t.Errorf("expected %d candles, got %d", len(expectedCandles), len(candles))
	}
}

// TestCachingCandleRepository_Find_CacheHit はキャッシュヒット時にRedisからデータを返し、内部リポジトリを呼ばないことを検証します。
func TestCachingCandleRepository_Find_CacheHit(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	cachedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	cachedJSON, _ := json.Marshal(cachedCandles)

	// キャッシュキーは outputsize を含まない
	mock.ExpectGet("candles:AAPL:1day").SetVal(string(cachedJSON))

	innerCalled := false
	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			innerCalled = true
			return nil, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	candles, err := repo.Find(context.Background(), "AAPL", "1day", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if innerCalled {
		t.Error("inner repository should not be called on cache hit")
	}
	if len(candles) != 1 {
		t.Errorf("expected 1 candle, got %d", len(candles))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_Find_CacheHit_Slices はキャッシュに複数件ある場合にoutputsize件にスライスして返すことを検証します。
func TestCachingCandleRepository_Find_CacheHit_Slices(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	// キャッシュには5件保存されている
	cachedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 100.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 101.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 102.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 103.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 104.0},
	}
	cachedJSON, _ := json.Marshal(cachedCandles)

	mock.ExpectGet("candles:AAPL:1day").SetVal(string(cachedJSON))

	inner := &mockReadWriteRepository{}
	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")

	// outputsize=3 を指定 → 先頭3件のみ返る
	candles, err := repo.Find(context.Background(), "AAPL", "1day", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 3 {
		t.Errorf("expected 3 candles, got %d", len(candles))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_Find_CacheMiss はキャッシュミス時にDBから全データを取得し、キャッシュに保存してoutputsize件を返すことを検証します。
func TestCachingCandleRepository_Find_CacheMiss(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	expectedJSON, _ := json.Marshal(expectedCandles)

	// Cache miss
	mock.ExpectGet("candles:AAPL:1day").RedisNil()
	// SetNXでキャッシュに保存（全データで保存）
	mock.ExpectSetNX("candles:AAPL:1day", expectedJSON, 5*time.Minute).SetVal(true)

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			// MaxOutputSize(5000) で呼ばれることを検証
			if outputsize != MaxOutputSize {
				t.Errorf("expected outputsize %d, got %d", MaxOutputSize, outputsize)
			}
			return expectedCandles, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	candles, err := repo.Find(context.Background(), "AAPL", "1day", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 1 {
		t.Errorf("expected 1 candle, got %d", len(candles))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_Find_InnerError は内部リポジトリがエラーを返した場合にそのエラーが伝播されることを検証します。
func TestCachingCandleRepository_Find_InnerError(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedErr := errors.New("database error")

	mock.ExpectGet("candles:AAPL:1day").RedisNil()

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return nil, expectedErr
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	_, err := repo.Find(context.Background(), "AAPL", "1day", 100)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

// TestCachingCandleRepository_Find_CorruptedCache は破損したキャッシュを検出・削除し、DBにフォールバックすることを検証します。
func TestCachingCandleRepository_Find_CorruptedCache(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	expectedJSON, _ := json.Marshal(expectedCandles)

	// Return invalid JSON from cache
	mock.ExpectGet("candles:AAPL:1day").SetVal("invalid json")
	// Delete corrupted cache
	mock.ExpectDel("candles:AAPL:1day").SetVal(1)
	// SetNXで新しいキャッシュを保存
	mock.ExpectSetNX("candles:AAPL:1day", expectedJSON, 5*time.Minute).SetVal(true)

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return expectedCandles, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	candles, err := repo.Find(context.Background(), "AAPL", "1day", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 1 {
		t.Errorf("expected 1 candle, got %d", len(candles))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_Find_CacheMiss_SetNXLosesRace はcache-miss後のSetNXが
// false（他インスタンスが既に新データを書き込み済み）を返しても、Findはエラーにならず
// inner.Findから取得したデータをそのまま返すことを検証します。
func TestCachingCandleRepository_Find_CacheMiss_SetNXLosesRace(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	expectedJSON, _ := json.Marshal(expectedCandles)

	// Cache miss
	mock.ExpectGet("candles:AAPL:1day").RedisNil()
	// SetNXが false を返す（他インスタンスが先に書き込み済み = キーが既に存在）
	mock.ExpectSetNX("candles:AAPL:1day", expectedJSON, 5*time.Minute).SetVal(false)

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return expectedCandles, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	candles, err := repo.Find(context.Background(), "AAPL", "1day", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 1 {
		t.Errorf("expected 1 candle, got %d", len(candles))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_UpsertBatch_NilRedis はRedisがnilの場合にUpsertBatchが内部リポジトリのみを呼び出すことを検証します。
func TestCachingCandleRepository_UpsertBatch_NilRedis(t *testing.T) {
	t.Parallel()

	innerCalled := false
	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			innerCalled = true
			return nil
		},
	}

	repo := NewCachingRepository(nil, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !innerCalled {
		t.Error("expected inner repository to be called")
	}
}

// TestCachingCandleRepository_UpsertBatch_InnerError は内部リポジトリのUpsertBatchエラーが伝播されることを検証します。
func TestCachingCandleRepository_UpsertBatch_InnerError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("upsert error")
	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return expectedErr
		},
	}

	repo := NewCachingRepository(nil, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day"},
	})

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

// TestCachingCandleRepository_UpsertBatch_EmptyCandles は空のローソク足データでUpsertBatchが正常に完了することを検証します。
func TestCachingCandleRepository_UpsertBatch_EmptyCandles(t *testing.T) {
	t.Parallel()

	rdb, _ := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCachingCandleRepository_UpsertBatch_InvalidatesCache はUpsertBatch後に
// 対象キーのキャッシュが削除されるのみで、ウォームアップ（inner.Findの呼び出し）が
// 行われないことを検証します（案A′: 再構築は次回Findのcache-miss経路に一本化）。
func TestCachingCandleRepository_UpsertBatch_InvalidatesCache(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	findCallCount := 0
	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			findCallCount++
			return nil, nil
		},
	}

	// 既存キャッシュを削除するのみ（SETは発行されない）
	mock.ExpectDel("candles:AAPL:1day").SetVal(1)

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findCallCount != 0 {
		t.Errorf("expected inner.Find not to be called (no warm-up), got %d calls", findCallCount)
	}
	// ExpectDel以外（Set等）が発行されていないことを担保
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_UpsertBatch_DeduplicatesDel は同一symbol+intervalの
// キャッシュキーに対するDELが重複せず1回のみ実行されることを検証します。
func TestCachingCandleRepository_UpsertBatch_DeduplicatesDel(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
	}

	// AAPL:1day が3件あっても DEL は1回のみ
	mock.ExpectDel("candles:AAPL:1day").SetVal(1)

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now()},
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now().Add(-24 * time.Hour)},
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now().Add(-48 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestSafeCacheKey はsafeCacheKey関数がRedisキーで問題となる文字を正しくエスケープすることを検証します。
func TestSafeCacheKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"AAPL", "AAPL"},
		{"BRK A", "BRK_A"},
		{"key:value", "key_value"},
		{"a b:c", "a_b_c"},
		{"", ""},
		{"  ", "__"},
		{"::", "__"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			result := safeCacheKey(tt.input)
			if result != tt.expected {
				t.Errorf("safeCacheKey(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
