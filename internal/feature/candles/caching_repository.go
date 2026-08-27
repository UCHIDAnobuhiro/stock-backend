package candles

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// latestCacheSize は銘柄・インターバルごとにRedisへ保存する最新足の件数です。
	// チャートの初回表示件数に合わせ、最大取得件数（MaxOutputSize）とは分離して管理します。
	latestCacheSize = 200

	// latestCacheVersion は最新足キャッシュのJSON形式バージョンです。
	// 旧形式のJSON配列と区別し、旧revisionが200件を全件キャッシュと誤認することを防ぎます。
	latestCacheVersion = 2
)

// latestCacheEntry は最新足キャッシュの保存形式です。
// 旧形式はCandleのJSON配列であり、新revisionは移行期間中も読み取り互換性を維持します。
type latestCacheEntry struct {
	Version int      `json:"version"`
	Candles []Candle `json:"candles"`
}

func newLatestCacheEntry(candles []Candle) latestCacheEntry {
	if candles == nil {
		candles = []Candle{}
	}
	return latestCacheEntry{Version: latestCacheVersion, Candles: candles}
}

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
		if err := c.rdb.Del(ctx, key).Err(); err != nil {
			slog.Warn("failed to invalidate candle cache",
				"symbol", si.symbol,
				"interval", si.interval,
				"error", err,
			)
		}
	}
	return nil
}

// Find はローソク足データを取得します。outputsizeがlatestCacheSize以下ならキャッシュを確認し、
// なければデータベースからlatestCacheSize件を取得して保存します。
// latestCacheSizeを超える要求は、件数の少ないキャッシュから不完全な結果を返さないよう
// キャッシュをバイパスしてデータベースから直接取得します。
func (c *CachingRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
	// outputsize は本来 handler で 1〜MaxOutputSize の範囲に検証済みだが、
	// cache-hit 経路と dbRepository.Find の挙動を一致させるため、
	// リポジトリ自身の不変条件としても防御的に検証する。
	if outputsize <= 0 || outputsize > MaxOutputSize {
		return nil, fmt.Errorf("find candles: %w", ErrInvalidOutputSize)
	}

	// Redisが未設定、または最新キャッシュの件数を超える要求の場合はキャッシュをバイパスする。
	// APIが既存の1〜MaxOutputSize件の要求を引き続き受け付けられるよう、最大取得件数は変更しない。
	if c.rdb == nil || outputsize > latestCacheSize {
		return c.inner.Find(ctx, symbol, interval, outputsize)
	}

	key := c.cacheKey(symbol, interval)

	// 1) キャッシュを確認
	if b, err := c.rdb.Get(ctx, key).Bytes(); err == nil && len(b) > 0 {
		if all, ok := decodeCachedCandles(b); ok {
			return sliceCandles(all, outputsize), nil
		}
		// 破損したキャッシュエントリを削除
		_ = c.rdb.Del(ctx, key).Err()
	}

	// 2) データベースにフォールバック（latestCacheSize件を取得してキャッシュに保存）
	all, err := c.inner.Find(ctx, symbol, interval, latestCacheSize)
	if err != nil {
		return nil, err
	}

	// 3) キャッシュに保存（ベストエフォート）
	// SetNX（SET key value EX <ttl> NX）を使い、キーが存在しない場合のみ書き込む。
	// ingestのDELの直後に他インスタンスが新データで既にキャッシュを再構築している
	// ケースで、本経路が読んだ（DEL前の）古いDBデータで上書きしてしまうのを防ぐ。
	entry := newLatestCacheEntry(all)
	if b, err := json.Marshal(entry); err == nil {
		_ = c.rdb.SetNX(ctx, key, b, c.ttl).Err()
	}

	return sliceCandles(all, outputsize), nil
}

// decodeCachedCandles は現行のバージョン付きオブジェクトと、移行前のJSON配列を読み取ります。
// 現行形式をオブジェクトにすることで、旧revisionはUnmarshalエラーとしてキャッシュを削除し、
// 最大MaxOutputSize件をDBから再取得できるため、ローリング更新とロールバック時も件数を誤りません。
func decodeCachedCandles(b []byte) ([]Candle, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, false
	}

	if b[0] == '[' {
		var legacy []Candle
		if err := json.Unmarshal(b, &legacy); err != nil {
			return nil, false
		}
		return legacy, true
	}

	var entry latestCacheEntry
	if err := json.Unmarshal(b, &entry); err != nil ||
		entry.Version != latestCacheVersion || entry.Candles == nil {
		return nil, false
	}
	return entry.Candles, true
}

// sliceCandles は全ローソク足データから先頭 outputsize 件を返します。
// outputsize は呼び出し元の Find で 1〜MaxOutputSize であることが検証済みのため、
// ここでは outputsize が件数以上の場合に全件返すことのみを扱います。
func sliceCandles(all []Candle, outputsize int) []Candle {
	if outputsize >= len(all) {
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
