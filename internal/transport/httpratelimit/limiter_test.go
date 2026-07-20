package httpratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/assert"
)

// setupEvalMock はAllow()のLuaスクリプト実行（EvalSha）のモック期待値を設定します。
// allowed=1, count=現在のカウント を返すように設定します。
// redismockはCustomMatchの前に引数数をチェックするため、ARGV分のダミー引数（5個）を渡します。
func setupEvalMock(mock redismock.ClientMock, key string, allowed int64, count int64) {
	match := mock.CustomMatch(func(expected, actual []interface{}) error {
		return nil
	})
	match.ExpectEvalSha(rateLimitScript.Hash(), []string{key},
		"_", "_", "_", "_", "_"). // ARGV[1]~[5]のダミー値（CustomMatchにより無視される）
		SetVal([]interface{}{allowed, count})
}

// setupEvalErrorMock はAllow()のLuaスクリプト実行がエラーを返すように設定します。
func setupEvalErrorMock(mock redismock.ClientMock, key string, err error) {
	match := mock.CustomMatch(func(expected, actual []interface{}) error {
		return nil
	})
	match.ExpectEvalSha(rateLimitScript.Hash(), []string{key},
		"_", "_", "_", "_", "_").SetErr(err)
}

// TestLimiter_Allow_NilRedis はRedisクライアントがnilの場合のPolicy別の挙動を検証します。
// FailOpenでは許可、FailClosedでは拒否（ServiceUnavailable）となることを確認します。
func TestLimiter_Allow_NilRedis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		policy     Policy
		wantAllow  bool
		wantSvcUnv bool
	}{
		{
			name:       "FailOpen: リクエストを許可する",
			policy:     FailOpen,
			wantAllow:  true,
			wantSvcUnv: false,
		},
		{
			name:       "FailClosed: リクエストを拒否しServiceUnavailableを立てる",
			policy:     FailClosed,
			wantAllow:  false,
			wantSvcUnv: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewLimiter(nil)
			result := limiter.Allow(context.Background(), "test:key", 5, time.Minute, tt.policy)

			assert.Equal(t, tt.wantAllow, result.Allowed)
			assert.Equal(t, tt.wantSvcUnv, result.ServiceUnavailable)
			assert.Zero(t, result.RetryAfter)
		})
	}
}

// TestLimiter_Allow はスライディングウィンドウレートリミットの許可・拒否判定を検証します。
// 制限内、制限到達時、制限超過時の各ケースをテストします。
func TestLimiter_Allow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		count       int64
		limit       int
		wantAllowed bool
		wantRetry   bool
	}{
		{
			name:        "under limit: request allowed",
			count:       2,
			limit:       5,
			wantAllowed: true,
			wantRetry:   false,
		},
		{
			name:        "at limit: request denied",
			count:       5,
			limit:       5,
			wantAllowed: false,
			wantRetry:   true,
		},
		{
			name:        "over limit: request denied",
			count:       10,
			limit:       5,
			wantAllowed: false,
			wantRetry:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb, mock := redismock.NewClientMock()
			defer func() { _ = rdb.Close() }()

			var allowed int64
			if tt.wantAllowed {
				allowed = 1
			}
			setupEvalMock(mock, "test:key", allowed, tt.count)

			limiter := NewLimiter(rdb)
			result := limiter.Allow(context.Background(), "test:key", tt.limit, time.Minute, FailOpen)

			assert.Equal(t, tt.wantAllowed, result.Allowed)
			if tt.wantRetry {
				assert.Equal(t, time.Minute, result.RetryAfter)
			} else {
				assert.Zero(t, result.RetryAfter)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestLimiter_Allow_RedisError_GracefulDegradation はRedis接続エラー時、FailOpenでは
// リクエストが許可されることを検証します。グレースフルデグレードにより、Redis障害時も
// サービスが継続動作することを保証します（candlesキャッシュ等の非クリティカル用途向け）。
func TestLimiter_Allow_RedisError_GracefulDegradation(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	connErr := fmt.Errorf("connection refused")
	setupEvalErrorMock(mock, "test:key", connErr)

	limiter := NewLimiter(rdb)
	result := limiter.Allow(context.Background(), "test:key", 5, time.Minute, FailOpen)

	assert.True(t, result.Allowed, "Redisエラー時はリクエストを許可すべき")
	assert.False(t, result.ServiceUnavailable)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLimiter_Allow_RedisError_FailClosed はRedis接続エラー時、FailClosedでは
// リクエストが拒否されServiceUnavailableが立つことを検証します（signup/login等の
// セキュリティクリティカルな用途向け）。RetryAfterは障害復旧時間が不明なためゼロ値のままとなります。
func TestLimiter_Allow_RedisError_FailClosed(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	connErr := fmt.Errorf("connection refused")
	setupEvalErrorMock(mock, "test:key", connErr)

	limiter := NewLimiter(rdb)
	result := limiter.Allow(context.Background(), "test:key", 5, time.Minute, FailClosed)

	assert.False(t, result.Allowed, "FailClosedかつRedisエラー時はリクエストを拒否すべき")
	assert.True(t, result.ServiceUnavailable)
	assert.Zero(t, result.RetryAfter, "障害復旧時間は不明なためRetryAfterはゼロ値のまま")
	assert.NoError(t, mock.ExpectationsWereMet())
}
