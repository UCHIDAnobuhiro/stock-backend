// Package clientratelimit はAPI呼び出しなどの操作頻度を制限するクライアント側レートリミッターを提供します。
package clientratelimit

import (
	"context"
	"log/slog"
	"time"
)

// RateLimiter はAPI呼び出しなどの操作頻度を制限します。
// 並行呼び出し非対応です。複数 goroutine から呼び出す場合は呼び出し側で排他制御してください。
type RateLimiter struct {
	limit     int           // インターバルあたりの最大操作回数
	interval  time.Duration // カウンターをリセットする時間間隔
	count     int
	lastReset time.Time
}

// NewRateLimiter は新しいRateLimiterインスタンスを生成します。
func NewRateLimiter(limit int, interval time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:     limit,
		interval:  interval,
		lastReset: time.Now(),
	}
}

// WaitIfNeeded はレートリミットに達しているか確認し、必要に応じて待機します。
// ctx がキャンセル/タイムアウトした場合は待機を中断し ctx.Err() を返します。
func (rl *RateLimiter) WaitIfNeeded(ctx context.Context) error {
	now := time.Now()
	// インターバルが経過していればカウンターをリセット
	if now.Sub(rl.lastReset) >= rl.interval {
		rl.count = 0
		rl.lastReset = now
	}

	rl.count++
	if rl.count > rl.limit {
		sleep := rl.interval - now.Sub(rl.lastReset)
		if sleep > 0 {
			slog.InfoContext(ctx, "rate limit reached, sleeping", "limit", rl.limit, "sleep_duration", sleep)
			timer := time.NewTimer(sleep)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				// 待機を完了せず抜けるため、増分済みのカウンタを巻き戻して呼び出しが
				// 発生しなかった状態に戻す（同一インスタンスを別 ctx で再利用する場合の状態破壊を防ぐ）。
				rl.count--
				return ctx.Err()
			}
		}
		// 待機後にリセット
		rl.count = 1
		rl.lastReset = time.Now()
	}
	return nil
}
