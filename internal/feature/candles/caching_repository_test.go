package candles

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
)

const testLatestCacheKey = "candles:AAPL:1day"

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal test value: %v", err)
	}
	return b
}

func latestCacheJSON(t *testing.T, candles []Candle) []byte {
	t.Helper()
	return mustMarshalJSON(t, newLatestCacheEntry(candles))
}

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
	cachedJSON := latestCacheJSON(t, cachedCandles)

	// 最新足キャッシュのキーは outputsize を含まない
	mock.ExpectGet(testLatestCacheKey).SetVal(string(cachedJSON))

	innerCalled := false
	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			innerCalled = true
			return nil, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	candles, err := repo.Find(context.Background(), "AAPL", "1day", latestCacheSize)
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

// TestCachingCandleRepository_Find_LegacyCacheHit は移行前のJSON配列キャッシュも読み取れることを検証します。
func TestCachingCandleRepository_Find_LegacyCacheHit(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	cachedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 100.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 101.0},
		{SymbolCode: "AAPL", Interval: "1day", Open: 102.0},
	}
	mock.ExpectGet(testLatestCacheKey).SetVal(string(mustMarshalJSON(t, cachedCandles)))

	innerCalled := false
	inner := &mockReadWriteRepository{
		findFn: func(context.Context, string, string, int) ([]Candle, error) {
			innerCalled = true
			return nil, nil
		},
	}

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	got, err := repo.Find(context.Background(), "AAPL", "1day", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if innerCalled {
		t.Error("inner repository should not be called on legacy cache hit")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 candles, got %d", len(got))
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
	cachedJSON := latestCacheJSON(t, cachedCandles)

	mock.ExpectGet(testLatestCacheKey).SetVal(string(cachedJSON))

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

			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })

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
			if got := mr.CommandCount(); got != 0 {
				t.Errorf("Redis should not be called, got %d commands", got)
			}
		})
	}
}

// TestCachingCandleRepository_Find_CacheMiss はキャッシュミス時にDBから最新足キャッシュ上限まで取得し、
// キャッシュに保存してoutputsize件を返すことを検証します。
func TestCachingCandleRepository_Find_CacheMiss(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedCandles := []Candle{
		{SymbolCode: "AAPL", Interval: "1day", Open: 150.0, Close: 155.0},
	}
	expectedJSON := latestCacheJSON(t, expectedCandles)

	// Cache miss
	mock.ExpectGet(testLatestCacheKey).RedisNil()
	// SetNXで最新足をキャッシュに保存
	mock.ExpectSetNX(testLatestCacheKey, expectedJSON, 5*time.Minute).SetVal(true)

	inner := &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			if outputsize != latestCacheSize {
				t.Errorf("expected outputsize %d, got %d", latestCacheSize, outputsize)
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

// TestCachingCandleRepository_Find_AboveLatestCacheSizeBypassesCache は最新足キャッシュ上限を
// 超える要求をRedisへ問い合わせず、要求件数のまま内部リポジトリへ渡すことを検証します。
func TestCachingCandleRepository_Find_AboveLatestCacheSizeBypassesCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outputsize int
	}{
		{name: "one above cache limit", outputsize: latestCacheSize + 1},
		{name: "API maximum", outputsize: MaxOutputSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })

			inner := &mockReadWriteRepository{
				findFn: func(_ context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
					if symbol != "AAPL" {
						t.Errorf("expected symbol AAPL, got %s", symbol)
					}
					if interval != "1day" {
						t.Errorf("expected interval 1day, got %s", interval)
					}
					if outputsize != tt.outputsize {
						t.Errorf("expected outputsize %d, got %d", tt.outputsize, outputsize)
					}
					return []Candle{{SymbolCode: symbol, Interval: interval}}, nil
				},
			}

			repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
			got, err := repo.Find(context.Background(), "AAPL", "1day", tt.outputsize)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 candle, got %d", len(got))
			}
			if got := mr.CommandCount(); got != 0 {
				t.Fatalf("Redis should not be called, got %d commands", got)
			}
		})
	}
}

// TestCachingCandleRepository_Find_InnerError は内部リポジトリがエラーを返した場合にそのエラーが伝播されることを検証します。
func TestCachingCandleRepository_Find_InnerError(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	expectedErr := errors.New("database error")

	mock.ExpectGet(testLatestCacheKey).RedisNil()

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
	expectedJSON := latestCacheJSON(t, expectedCandles)

	// Return invalid JSON from cache
	mock.ExpectGet(testLatestCacheKey).SetVal("invalid json")
	// Delete corrupted cache
	mock.ExpectDel(testLatestCacheKey).SetVal(1)
	// SetNXで新しいキャッシュを保存
	mock.ExpectSetNX(testLatestCacheKey, expectedJSON, 5*time.Minute).SetVal(true)

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
	expectedJSON := latestCacheJSON(t, expectedCandles)

	// Cache miss
	mock.ExpectGet(testLatestCacheKey).RedisNil()
	// SetNXが false を返す（他インスタンスが先に書き込み済み = キーが既に存在）
	mock.ExpectSetNX(testLatestCacheKey, expectedJSON, 5*time.Minute).SetVal(false)

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

// TestLatestCacheEntry_LegacyDecoderRejectsCurrentFormat は旧revisionが現行の200件キャッシュを
// JSON配列として誤認せず、キャッシュミスへフォールバックできることを検証します。
func TestLatestCacheEntry_LegacyDecoderRejectsCurrentFormat(t *testing.T) {
	t.Parallel()

	b := latestCacheJSON(t, []Candle{{SymbolCode: "AAPL", Interval: "1day"}})
	var legacy []Candle
	if err := json.Unmarshal(b, &legacy); err == nil {
		t.Fatal("legacy array decoder should reject current cache format")
	}
}

// TestDecodeCachedCandles_RejectsInvalidEnvelope は未対応・不完全なenvelopeを
// キャッシュヒットとして扱わないことを検証します。
func TestDecodeCachedCandles_RejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "unsupported version",
			raw: mustMarshalJSON(t, latestCacheEntry{
				Version: latestCacheVersion + 1,
				Candles: []Candle{},
			}),
		},
		{
			name: "missing candles",
			raw:  mustMarshalJSON(t, map[string]any{"version": latestCacheVersion}),
		},
		{
			name: "null candles",
			raw:  mustMarshalJSON(t, map[string]any{"version": latestCacheVersion, "candles": nil}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if candles, ok := decodeCachedCandles(tt.raw); ok {
				t.Fatalf("expected invalid envelope to be rejected, got %v", candles)
			}
		})
	}
}

// TestDecodeCachedCandles_AcceptsEmptyCurrentCache はデータがない場合の空配列を
// 有効なキャッシュとして扱い、DBへの反復問い合わせを防げることを検証します。
func TestDecodeCachedCandles_AcceptsEmptyCurrentCache(t *testing.T) {
	t.Parallel()

	b := latestCacheJSON(t, nil)
	candles, ok := decodeCachedCandles(b)
	if !ok {
		t.Fatal("expected empty current cache to be accepted")
	}
	if candles == nil || len(candles) != 0 {
		t.Fatalf("expected non-nil empty candles, got %v", candles)
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
	mock.ExpectDel(testLatestCacheKey).SetVal(1)

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

// TestCachingCandleRepository_UpsertBatch_CacheInvalidationError はキャッシュ削除に
// 失敗しても、DBへの書き込みが成功していればUpsertBatchを成功として扱うことを検証します。
func TestCachingCandleRepository_UpsertBatch_CacheInvalidationError(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	inner := &mockReadWriteRepository{
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			return nil
		},
	}

	mock.ExpectDel(testLatestCacheKey).SetErr(errors.New("redis unavailable"))

	repo := NewCachingRepository(rdb, 5*time.Minute, inner, "candles")
	err := repo.UpsertBatch(context.Background(), []Candle{
		{SymbolCode: "AAPL", Interval: "1day"},
	})
	if err != nil {
		t.Fatalf("cache invalidation error should not fail the DB write: %v", err)
	}
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
	mock.ExpectDel(testLatestCacheKey).SetVal(1)

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
