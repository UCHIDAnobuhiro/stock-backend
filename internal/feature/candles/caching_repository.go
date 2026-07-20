package candles

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultCacheTTL はキャッシュが汚染された場合やDEL失敗時に、古いデータが
// 残り続けないためのセーフティネットTTL。通常運用ではingestのUpsertBatchが
// 対象キーをDELし、次回Find時のcache-miss経路でキャッシュが再構築されるため、
// このTTLはあくまでフォールバックとしてのみ機能する。
const DefaultCacheTTL = 24 * time.Hour

// readWriteRepository はCachingRepositoryが内部で必要とする読み書きインターフェースです。
type readWriteRepository interface {
	Repository      // usecase.go（Find）
	WriteRepository // ingest.go（UpsertBatch）
}

// CachingRepository はRepositoryにRedisキャッシュをデコレータパターンで追加します。
// 基盤となるリポジトリを変更せずに、透過的にキャッシュを追加します。
type CachingRepository struct {
	inner     readWriteRepository
	rdb       *redis.Client
	ttl       time.Duration
	namespace string
}

// NewCachingRepository はRepositoryにRedisキャッシュを追加するデコレータを生成します。
// ttlが0の場合はデフォルト5分、namespaceが空の場合は"candles"を使用します。
func NewCachingRepository(rdb *redis.Client, ttl time.Duration, inner readWriteRepository, namespace string) *CachingRepository {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if namespace == "" {
		namespace = "candles"
	}
	return &CachingRepository{
		inner:     inner,
		rdb:       rdb,
		ttl:       ttl,
		namespace: namespace,
	}
}

// UpsertBatch はローソク足データを挿入または更新し、対象キーのキャッシュを削除します。
// ここでは削除のみを行い、キャッシュの再構築は行いません（ウォームアップの廃止）。
//
// ingestバッチとAPIサーバーは別インスタンスで動作するため、UpsertBatch完了直後に
// inner.Findでウォームアップすると、他インスタンス上のFindが本コミット前の古いDBデータを
// 読み取り、その古いデータでキャッシュをSETし直してウォームアップ結果を上書きしてしまう
// 競合が起こり得る（「書くのは読み手だけ、消すのは書き手だけ」の原則）。
// 再構築は次回Findのcache-miss経路に一本化し、書き込みはSetNXでベストエフォートに行う。
func (c *CachingRepository) UpsertBatch(ctx context.Context, candles []Candle) error {
	// まず基盤リポジトリにUpsert
	if err := c.inner.UpsertBatch(ctx, candles); err != nil {
		return err
	}
	// Redisが未設定またはデータがない場合は早期リターン
	if c.rdb == nil || len(candles) == 0 {
		return nil
	}

	// 影響を受ける symbol+interval を収集
	type symbolInterval struct {
		symbol   string
		interval string
	}
	seen := map[symbolInterval]struct{}{}
	for _, cd := range candles {
		seen[symbolInterval{cd.SymbolCode, cd.Interval}] = struct{}{}
	}

	// 各 symbol+interval のキャッシュを削除するのみ（再構築は次回Findに委ねる）
	for si := range seen {
		key := c.cacheKey(si.symbol, si.interval)
		_ = c.rdb.Del(ctx, key).Err() // ベストエフォート
	}
	return nil
}

// Find はローソク足データを取得します。まずキャッシュを確認し、なければデータベースにフォールバックします。
// キャッシュには全データ（最大MaxOutputSize件）を保存し、outputsize件にスライスして返します。
func (c *CachingRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
	// Redisが未設定の場合はキャッシュをバイパス
	if c.rdb == nil {
		return c.inner.Find(ctx, symbol, interval, outputsize)
	}

	key := c.cacheKey(symbol, interval)

	// 1) キャッシュを確認
	if b, err := c.rdb.Get(ctx, key).Bytes(); err == nil && len(b) > 0 {
		var all []Candle
		if err := json.Unmarshal(b, &all); err == nil {
			return sliceCandles(all, outputsize), nil
		}
		// 破損したキャッシュエントリを削除
		_ = c.rdb.Del(ctx, key).Err()
	}

	// 2) データベースにフォールバック（全データ取得してキャッシュに保存）
	all, err := c.inner.Find(ctx, symbol, interval, MaxOutputSize)
	if err != nil {
		return nil, err
	}

	// 3) キャッシュに保存（ベストエフォート）
	// SetNX（SET key value EX <ttl> NX）を使い、キーが存在しない場合のみ書き込む。
	// ingestのDELの直後に他インスタンスが新データで既にキャッシュを再構築している
	// ケースで、本経路が読んだ（DEL前の）古いDBデータで上書きしてしまうのを防ぐ。
	if b, err := json.Marshal(all); err == nil {
		_ = c.rdb.SetNX(ctx, key, b, c.ttl).Err()
	}

	return sliceCandles(all, outputsize), nil
}

// sliceCandles は全ローソク足データから先頭 outputsize 件を返します。
func sliceCandles(all []Candle, outputsize int) []Candle {
	if outputsize <= 0 || outputsize >= len(all) {
		return all
	}
	return all[:outputsize]
}

// cacheKey はキャッシュキーを生成します。
func (c *CachingRepository) cacheKey(symbol, interval string) string {
	return fmt.Sprintf("%s:%s:%s",
		c.namespace,
		safeCacheKey(symbol),
		safeCacheKey(interval),
	)
}

// safeCacheKey はRedisキーで問題となる文字をエスケープします。
func safeCacheKey(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ":", "_")
	return s
}
