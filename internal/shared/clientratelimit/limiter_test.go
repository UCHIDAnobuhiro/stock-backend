package clientratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRateLimiter_WaitIfNeeded はレートリミット待機・カウンタリセット・context 連携の挙動を検証します。
func TestRateLimiter_WaitIfNeeded(t *testing.T) {
	t.Run("limit 未到達の場合は待機しない", func(t *testing.T) {
		rl := NewRateLimiter(3, 100*time.Millisecond)
		start := time.Now()
		for i := range 3 {
			if err := rl.WaitIfNeeded(context.Background()); err != nil {
				t.Fatalf("unexpected error on call %d: %v", i, err)
			}
		}
		if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
			t.Errorf("limit 未到達なのに待機した: elapsed=%v", elapsed)
		}
	})

	t.Run("limit 到達後の呼び出しはインターバル経過まで待機する", func(t *testing.T) {
		interval := 80 * time.Millisecond
		rl := NewRateLimiter(2, interval)
		ctx := context.Background()

		// 2回までは即時リターン
		for i := range 2 {
			if err := rl.WaitIfNeeded(ctx); err != nil {
				t.Fatalf("unexpected error on call %d: %v", i, err)
			}
		}

		// 3回目は待機する
		start := time.Now()
		if err := rl.WaitIfNeeded(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		elapsed := time.Since(start)
		if elapsed < interval/2 {
			t.Errorf("待機が短すぎる: elapsed=%v, interval=%v", elapsed, interval)
		}
		if elapsed > interval*3 {
			t.Errorf("待機が長すぎる: elapsed=%v, interval=%v", elapsed, interval)
		}
	})

	t.Run("インターバル経過後はカウンタがリセットされる", func(t *testing.T) {
		interval := 30 * time.Millisecond
		rl := NewRateLimiter(2, interval)
		ctx := context.Background()

		if err := rl.WaitIfNeeded(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// インターバル経過を待ってからもう一度
		time.Sleep(interval + 10*time.Millisecond)

		start := time.Now()
		if err := rl.WaitIfNeeded(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
			t.Errorf("リセット後なのに待機した: elapsed=%v", elapsed)
		}
	})
}

// TestRateLimiter_WaitIfNeeded_ContextCancellation はctxキャンセル/タイムアウト連携をテーブル駆動で検証します。
func TestRateLimiter_WaitIfNeeded_ContextCancellation(t *testing.T) {
	interval := 200 * time.Millisecond

	// limit を到達させた状態のリミッターを返すヘルパー
	saturate := func() *RateLimiter {
		rl := NewRateLimiter(1, interval)
		if err := rl.WaitIfNeeded(context.Background()); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		return rl
	}

	testCases := []struct {
		name         string
		setupCtx     func() (context.Context, context.CancelFunc)
		saturated    bool          // limit 到達済みの状態で呼ぶか
		wantErr      error         // errors.Is で照合
		wantElapseLT time.Duration // 経過時間の上限（待機が打ち切られるはず）
	}{
		{
			name: "limit 未到達の即時キャンセル ctx は待機なしで nil",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			saturated:    false,
			wantErr:      nil,
			wantElapseLT: 50 * time.Millisecond,
		},
		{
			name: "limit 到達中の即時キャンセル ctx は ctx.Err を返す",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			saturated:    true,
			wantErr:      context.Canceled,
			wantElapseLT: 50 * time.Millisecond,
		},
		{
			name: "待機中に cancel されると Canceled で抜ける",
			setupCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					time.Sleep(20 * time.Millisecond)
					cancel()
				}()
				return ctx, cancel
			},
			saturated:    true,
			wantErr:      context.Canceled,
			wantElapseLT: interval / 2,
		},
		{
			name: "待機中にデッドラインを超えると DeadlineExceeded で抜ける",
			setupCtx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 30*time.Millisecond)
			},
			saturated:    true,
			wantErr:      context.DeadlineExceeded,
			wantElapseLT: interval / 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var rl *RateLimiter
			if tc.saturated {
				rl = saturate()
			} else {
				rl = NewRateLimiter(1, interval)
			}

			ctx, cancel := tc.setupCtx()
			defer cancel()

			start := time.Now()
			err := rl.WaitIfNeeded(ctx)
			elapsed := time.Since(start)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			if elapsed > tc.wantElapseLT {
				t.Errorf("elapsed = %v, want < %v", elapsed, tc.wantElapseLT)
			}
		})
	}
}

// TestRateLimiter_WaitIfNeeded_CountRollbackOnCancel は ctx キャンセルで待機を抜けた際に
// カウンタが「呼び出し前の値」に巻き戻されることを検証します。
// 巻き戻しがないと count が limit を超えて残り、内部状態が不整合になります。
func TestRateLimiter_WaitIfNeeded_CountRollbackOnCancel(t *testing.T) {
	interval := 200 * time.Millisecond
	rl := NewRateLimiter(1, interval)

	// 1回目で limit 到達。ここで count は limit と同じ値になる。
	if err := rl.WaitIfNeeded(context.Background()); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	countBefore := rl.count

	// 2回目は cancelled ctx で待機を打ち切る。
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rl.WaitIfNeeded(cancelCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}

	// count が「呼び出し前」の値に巻き戻されていることを確認する。
	if rl.count != countBefore {
		t.Errorf("count = %d, want %d (ctx キャンセル時のロールバックが効いていない)", rl.count, countBefore)
	}
}
