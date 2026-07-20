// Package httpratelimit はRedisベースのスライディングウィンドウレートリミッターを提供します。
package httpratelimit

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result はレートリミットチェックの結果を保持します。
type Result struct {
	Allowed bool
	// RetryAfter はレートリミット超過（429）時の再試行までの推奨待機時間です。
	// ServiceUnavailable が true の場合はゼロ値のままです（障害復旧時間は不明なため）。
	RetryAfter time.Duration
	// ServiceUnavailable はRedis未接続・障害により判定自体ができず、
	// Policy=FailClosed によりリクエストを拒否したことを示します。
	// 呼び出し側はこれを見て 429（レートリミット超過）と 503（基盤障害）を区別します。
	ServiceUnavailable bool
}

// Policy はRedis未接続・障害時にレートリミット判定をどう扱うかを表します。
// ゼロ値は FailClosed です（secure by default）。呼び出し側が Policy の指定を忘れても
// 安全側（拒否）に倒れるようにするための設計であり、FailOpen は非クリティカルな用途に対する
// 明示的なオプトインとして扱います。
type Policy int

const (
	// FailClosed はRedis障害時にリクエストを拒否します（セキュリティ優先）。
	// signup/login等、ブルートフォース対策が必須なクリティカルな用途向けです。
	// ゼロ値であり、Policy未指定時のデフォルト挙動です。
	FailClosed Policy = iota
	// FailOpen はRedis障害時にリクエストを許可します（可用性優先）。
	// candlesキャッシュ等、非クリティカルな用途向けのグレースフルデグレードです。
	// secure by defaultの方針により、利用する場合は明示的に指定する必要があります。
	FailOpen
)

// Limiter はRedisソート済みセットを使用したスライディングウィンドウレートリミッターです。
// rdbがnilまたはRedis障害時の挙動はAllowに渡すPolicyにより切り替わります
// （FailOpen: 許可してグレースフルデグレード / FailClosed: 503相当で拒否）。
type Limiter struct {
	rdb *redis.Client
}

// NewLimiter はLimiterの新しいインスタンスを生成します。
func NewLimiter(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

// rateLimitScript はスライディングウィンドウレートリミットをRedis上で原子的に実行するLuaスクリプトです。
// KEYS[1]: レートリミットキー
// ARGV[1]: ウィンドウ開始タイムスタンプ（ナノ秒）
// ARGV[2]: 最大リクエスト数（limit）
// ARGV[3]: 現在のタイムスタンプ（ナノ秒、ZADDのスコア）
// ARGV[4]: メンバー値（一意性確保用）
// ARGV[5]: TTL秒数
// 戻り値: {allowed (0 or 1), count}
var rateLimitScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local count = redis.call('ZCARD', KEYS[1])
if count < tonumber(ARGV[2]) then
  redis.call('ZADD', KEYS[1], tonumber(ARGV[3]), ARGV[4])
  redis.call('EXPIRE', KEYS[1], tonumber(ARGV[5]))
  return {1, count}
end
return {0, count}
`)

// Allow は指定されたキーに対してリクエストが許可されるかチェックします。
// keyはレートリミットの識別子（例: "rl:login:ip:192.168.1.1"）、
// limitはウィンドウ内の最大リクエスト数、windowはスライディングウィンドウの時間幅です。
// policyはRedis未接続・障害時の挙動を指定します（FailOpen: 許可 / FailClosed: 拒否）。
// Luaスクリプトにより判定と追加を原子的に実行し、レースコンディションを防止します。
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration, policy Policy) Result {
	if l == nil || l.rdb == nil {
		if policy == FailClosed {
			slog.Warn("rate limiter unavailable (no redis), rejecting request",
				"prefix", keyPrefix(key))
			return Result{Allowed: false, ServiceUnavailable: true}
		}
		return Result{Allowed: true}
	}

	now := time.Now()
	nowNano := now.UnixNano()
	windowStart := now.Add(-window).UnixNano()

	var randBuf [8]byte
	_, _ = rand.Read(randBuf[:])
	member := fmt.Sprintf("%d:%x", nowNano, randBuf[:])

	ttlSeconds := int(window.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}

	res, err := rateLimitScript.Run(ctx, l.rdb, []string{key},
		fmt.Sprintf("%d", windowStart),
		limit,
		fmt.Sprintf("%d", nowNano),
		member,
		ttlSeconds,
	).Int64Slice()

	if err != nil || len(res) < 1 {
		if policy == FailClosed {
			slog.Error("rate limit check failed, rejecting request",
				"prefix", keyPrefix(key), "error", err)
			return Result{Allowed: false, ServiceUnavailable: true}
		}
		slog.Warn("rate limit check failed, allowing request",
			"prefix", keyPrefix(key), "error", err)
		return Result{Allowed: true}
	}

	if res[0] == 1 {
		return Result{Allowed: true}
	}

	return Result{
		Allowed:    false,
		RetryAfter: window,
	}
}

// scriptHash はLuaスクリプトのSHA1ハッシュを返します（テストヘルパー ExpectAllow から利用）。
func scriptHash() string {
	return rateLimitScript.Hash()
}

// keyPrefix はレートリミットキーからPIIを含まないプレフィックス部分を抽出します。
// 固定の先頭3セグメントのみを返すことで、IPv6アドレス等のコロンを含む値でも安全です。
// 例: "rl:login:email:user@example.com" → "rl:login:email"
// 例: "rl:login:ip:2001:db8::1"        → "rl:login:ip"
func keyPrefix(key string) string {
	parts := strings.SplitN(key, ":", 4)
	if len(parts) >= 3 {
		return strings.Join(parts[:3], ":")
	}
	if len(parts) >= 1 {
		return parts[0]
	}
	return "unknown"
}
