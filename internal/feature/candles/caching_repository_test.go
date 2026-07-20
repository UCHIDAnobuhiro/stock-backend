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

// TestCachingCandleRepository_Find_InvalidOutputSize は outputsize が範囲外（0以下・MaxOutputSize超）の場合に
// キャッシュヒット状態であっても ErrInvalidOutputSize を返し、inner.Find を呼ばないことを検証します
// （cache-hit経路とdbRepository.Find経路で挙動を一致させるための防御的バリデーション）。
func TestCachingCandleRepository_Find_InvalidOutputSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outputsize int
	}{
		{name: "zero", outputsize: 0},
		{name: "negative", outputsize: -1},
		{name: "exceeds max", outputsize: MaxOutputSize + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb, mock := redismock.NewClientMock()
			defer func() { _ = rdb.Close() }()

			cachedCandles := []Candle{
				{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
			}
			cachedJSON, _ := json.Marshal(cachedCandles)

			// キャッシュにヒットする状態をセットアップするが、outputsize検証は
			// キャッシュ参照より前に行われるため Redis へのアクセスは発生しない。
			mock.ExpectGet("candles:AAPL:1day").SetVal(string(cachedJSON))

			innerCalled := false
			inner := &mockReadWriteRepository{
				findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
					innerCalled = true
					return nil, nil
				},
			}

			repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
			candles, err := repo.Find(context.Background(), "AAPL", "1day", tt.outputsize)

			if !errors.Is(err, ErrInvalidOutputSize) {
				t.Errorf("expected ErrInvalidOutputSize, got %v", err)
			}
			if candles != nil {
				t.Errorf("expected nil candles, got %v", candles)
			}
			if innerCalled {
				t.Error("inner repository should not be called for invalid outputsize")
			}
		})
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
	// Set cache after fetching from inner (全データで保存)
	mock.ExpectSet("candles:AAPL:1day", expectedJSON, 5*time.Minute).SetVal("OK")

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
	// Set new cache after fetching from inner
	mock.ExpectSet("candles:AAPL:1day", expectedJSON, 5*time.Minute).SetVal("OK")

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

// TestCachingCandleRepository_UpsertBatch_CacheWarmUp はUpsertBatch後にキャッシュが削除され最新データで再生成されることを検証します。
func TestCachingCandleRepository_UpsertBatch_CacheWarmUp(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	warmCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	warmJSON, _ := json.Marshal(warmCandles)

	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return warmCandles, nil
		},
	}

	// 既存キャッシュを削除してから最新データで再生成
	mock.ExpectDel("candles:AAPL:1day").SetVal(1)
	mock.ExpectSet("candles:AAPL:1day", warmJSON, 5*time.Minute).SetVal("OK")

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestCachingCandleRepository_UpsertBatch_DeduplicatesWarmUp は同一symbol+intervalのウォームアップが重複せず1回のみ実行されることを検証します。
func TestCachingCandleRepository_UpsertBatch_DeduplicatesWarmUp(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	warmCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0},
	}
	warmJSON, _ := json.Marshal(warmCandles)

	findCallCount := 0
	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			findCallCount++
			return warmCandles, nil
		},
	}

	// AAPL:1day が3件あっても DEL と SET は1回ずつのみ
	mock.ExpectDel("candles:AAPL:1day").SetVal(1)
	mock.ExpectSet("candles:AAPL:1day", warmJSON, 5*time.Minute).SetVal("OK")

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now()},
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now().Add(-24 * time.Hour)},
		{SymbolCode: "AAPL", Interval: "1day", Time: time.Now().Add(-48 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findCallCount != 1 {
		t.Errorf("expected inner.Find to be called once, got %d", findCallCount)
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
