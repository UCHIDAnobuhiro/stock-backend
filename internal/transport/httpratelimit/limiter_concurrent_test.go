package httpratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLimiter_Allow_Concurrent は同一キーに対して limit を超える数の Allow を並行呼び出しし、
// Luaスクリプト（ZCARD→ZADDの原子的実行）により許可数がちょうど limit 件に収まることを検証します。
// member 値は "<nowNano>:<8バイト乱数の16進数>" で構成されるため、同一ナノ秒に複数goroutineが
// 実行してもZADDのメンバーが衝突する（＝重複としてカウントされない）可能性は実質的にゼロであり、
// 許可数の厳密な一致（ちょうどlimit件）をアサートしてよい。
func TestLimiter_Allow_Concurrent(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const (
		limit      = 10
		window     = time.Minute
		key        = "rl:test:concurrent"
		goroutines = 50
	)

	limiter := NewLimiter(rdb)

	var (
		wg              sync.WaitGroup
		allowedCount    atomic.Int64
		deniedCount     atomic.Int64
		svcUnavailable  atomic.Int64
		badRetryAfterMu sync.Mutex
		badRetryAfters  []time.Duration
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := limiter.Allow(context.Background(), key, limit, window, FailClosed)
			if result.ServiceUnavailable {
				svcUnavailable.Add(1)
				return
			}
			if result.Allowed {
				allowedCount.Add(1)
				return
			}
			deniedCount.Add(1)
			if result.RetryAfter != window {
				badRetryAfterMu.Lock()
				badRetryAfters = append(badRetryAfters, result.RetryAfter)
				badRetryAfterMu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Zero(t, svcUnavailable.Load(), "ServiceUnavailableが発生してはならない（miniredisは常に到達可能）")
	assert.Equal(t, int64(limit), allowedCount.Load(), "許可数はちょうどlimit件になるべき")
	assert.Equal(t, int64(goroutines-limit), deniedCount.Load(), "拒否数はgoroutines-limit件になるべき")
	assert.Empty(t, badRetryAfters, "拒否された全リクエストのRetryAfterはwindowと一致すべき")
}
