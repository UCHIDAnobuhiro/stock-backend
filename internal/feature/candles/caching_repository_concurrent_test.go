package candles

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// versionedInnerRepository はmockReadWriteRepositoryをラップし、「現在のバージョン」を
// sync.Mutexで保護しつつ保持するテスト用インナーリポジトリです。
// 各バージョンのCandleはすべて同じCloseの値を持たせることで、Find結果が
// 単一バージョンの完全なスナップショットになっているかを検証できるようにします。
type versionedInnerRepository struct {
	mu      sync.Mutex
	current []Candle
	*mockReadWriteRepository
}

// newVersionedInnerRepository はversion=0の初期データを持つversionedInnerRepositoryを生成します。
func newVersionedInnerRepository(symbol, interval string, size int) *versionedInnerRepository {
	v := &versionedInnerRepository{}
	v.setVersion(symbol, interval, size, 0)
	v.mockReadWriteRepository = &mockReadWriteRepository{
		findFn: func(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
			return sliceCandles(v.snapshot(), outputsize), nil
		},
		upsertBatchFn: func(ctx context.Context, candles []Candle) error {
			v.mu.Lock()
			defer v.mu.Unlock()
			v.current = candles
			return nil
		},
	}
	return v
}

// setVersion は current を version 番号をCloseに埋め込んだ candles で置き換えます。
func (v *versionedInnerRepository) setVersion(symbol, interval string, size int, version int) {
	candles := make([]Candle, size)
	for i := range candles {
		candles[i] = Candle{
			SymbolCode: symbol,
			Interval:   interval,
			Time:       time.Now().Add(-time.Duration(i) * 24 * time.Hour),
			Close:      float64(version), // バージョン番号をCloseに埋め込み、スナップショットの一貫性検証に使う
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.current = candles
}

// snapshot は現在の current のコピーを返します（呼び出し元が結果を変更しても
// current 自体に影響を与えないようにするため）。
func (v *versionedInnerRepository) snapshot() []Candle {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Candle, len(v.current))
	copy(out, v.current)
	return out
}

// TestCachingRepository_ConcurrentFindAndUpsert はFindとUpsertBatchを並行実行しても
// レースコンディション（-race）が検出されず、各Find結果が単一バージョンの完全な
// スナップショットになっていることを検証します。
// SetNX/Del方式の設計上、実行中の一時的な古いキャッシュ読み取り（stale read）は
// 許容範囲であるため、ここでは「読み取り結果の内部一貫性」と「全goroutine終了後の
// 最終的な整合性（Del→cache miss→再構築）」のみを検証します。
func TestCachingRepository_ConcurrentFindAndUpsert(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const (
		symbol           = "AAPL"
		interval         = "1day"
		candleCount      = 5
		findGoroutines   = 10
		findIterations   = 20
		upsertGoroutines = 2
		upsertIterations = 20
	)

	inner := newVersionedInnerRepository(symbol, interval, candleCount)
	repo := NewCachingRepository(rdb, DefaultCacheTTL, inner, "candles-concurrent-test")

	ctx := context.Background()

	var (
		wg    sync.WaitGroup
		errMu sync.Mutex
		errs  []error

		versionMu  sync.Mutex
		versionSeq int
	)
	nextVersion := func() int {
		versionMu.Lock()
		defer versionMu.Unlock()
		versionSeq++
		return versionSeq
	}
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		errs = append(errs, err)
	}

	// Find側: 同一symbol/interval/outputsizeで並行に読み取る
	for i := 0; i < findGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < findIterations; j++ {
				candles, err := repo.Find(ctx, symbol, interval, candleCount)
				if err != nil {
					recordErr(fmt.Errorf("Find: %w", err))
					continue
				}
				// スナップショットの一貫性: 件数とCloseの値が揃っているか検証
				if len(candles) != candleCount {
					recordErr(fmt.Errorf("Find returned %d candles, want %d", len(candles), candleCount))
					continue
				}
				want := candles[0].Close
				for _, c := range candles {
					if c.Close != want {
						recordErr(fmt.Errorf("Find result mixed versions: got Close values %v", closesOf(candles)))
						break
					}
				}
			}
		}()
	}

	// Upsert側: バージョンを更新しながら並行に書き込む
	for i := 0; i < upsertGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < upsertIterations; j++ {
				inner.setVersion(symbol, interval, candleCount, nextVersion())
				if err := repo.UpsertBatch(ctx, inner.snapshot()); err != nil {
					recordErr(fmt.Errorf("UpsertBatch: %w", err))
				}
			}
		}()
	}

	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("expected no errors during concurrent Find/UpsertBatch, got %d errors, first: %v", len(errs), errs[0])
	}

	// 最終確認: sentinelバージョンでUpsertBatchした後、Findがその内容を反映していること
	// （Del→cache miss→再構築の経路が正しく機能していることを検証）
	const sentinelVersion = -1
	inner.setVersion(symbol, interval, candleCount, sentinelVersion)
	if err := repo.UpsertBatch(ctx, inner.snapshot()); err != nil {
		t.Fatalf("final UpsertBatch failed: %v", err)
	}

	final, err := repo.Find(ctx, symbol, interval, candleCount)
	if err != nil {
		t.Fatalf("final Find failed: %v", err)
	}
	if len(final) != candleCount {
		t.Fatalf("expected %d candles in final snapshot, got %d", candleCount, len(final))
	}
	for _, c := range final {
		if c.Close != float64(sentinelVersion) {
			t.Fatalf("expected final snapshot to reflect sentinel version %d, got Close=%v", sentinelVersion, c.Close)
		}
	}
}

// closesOf はエラーメッセージ用にCandleスライスからCloseの値だけを抽出します。
func closesOf(candles []Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = c.Close
	}
	return out
}
