package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRedisOAuthStateStore はminiredisに接続したredisOAuthStateStoreを生成するテスト用ヘルパーです。
// サブテストごとに独立したminiredisインスタンスを起動し、状態を分離します。
func newTestRedisOAuthStateStore(t *testing.T) (*redisOAuthStateStore, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return NewRedisOAuthStateStore(rdb), mr
}

func TestRedisOAuthStateStore_SaveState(t *testing.T) {
	t.Parallel()

	t.Run("success: stores value under the state key with the given TTL", func(t *testing.T) {
		t.Parallel()

		store, mr := newTestRedisOAuthStateStore(t)

		err := store.SaveState(context.Background(), "state-1", "verifier-1", 5*time.Minute)

		require.NoError(t, err)
		got, err := mr.Get("oauth:state:state-1")
		require.NoError(t, err)
		assert.Equal(t, "verifier-1", got)
		assert.Equal(t, 5*time.Minute, mr.TTL("oauth:state:state-1"))
	})

	t.Run("error: redis is unavailable", func(t *testing.T) {
		t.Parallel()

		store, mr := newTestRedisOAuthStateStore(t)
		mr.Close()

		err := store.SaveState(context.Background(), "state-1", "verifier-1", 5*time.Minute)

		assert.Error(t, err)
	})
}

func TestRedisOAuthStateStore_ConsumeState(t *testing.T) {
	t.Parallel()

	t.Run("success: returns and deletes the stored verifier", func(t *testing.T) {
		t.Parallel()

		store, _ := newTestRedisOAuthStateStore(t)
		require.NoError(t, store.SaveState(context.Background(), "state-1", "verifier-1", 5*time.Minute))

		got, err := store.ConsumeState(context.Background(), "state-1")
		require.NoError(t, err)
		assert.Equal(t, "verifier-1", got)

		// 一度消費したstateはatomicに削除されているため、再度の取得はErrStateNotFoundとなる（リプレイ防止）。
		_, err = store.ConsumeState(context.Background(), "state-1")
		assert.ErrorIs(t, err, ErrStateNotFound)
	})

	t.Run("error: unknown state", func(t *testing.T) {
		t.Parallel()

		store, _ := newTestRedisOAuthStateStore(t)

		_, err := store.ConsumeState(context.Background(), "unknown-state")

		assert.ErrorIs(t, err, ErrStateNotFound)
	})

	t.Run("error: expired state", func(t *testing.T) {
		t.Parallel()

		store, mr := newTestRedisOAuthStateStore(t)
		require.NoError(t, store.SaveState(context.Background(), "state-1", "verifier-1", 10*time.Minute))

		mr.FastForward(11 * time.Minute)

		_, err := store.ConsumeState(context.Background(), "state-1")
		assert.ErrorIs(t, err, ErrStateNotFound)
	})

	t.Run("error: redis is unavailable", func(t *testing.T) {
		t.Parallel()

		store, mr := newTestRedisOAuthStateStore(t)
		mr.Close()

		_, err := store.ConsumeState(context.Background(), "state-1")

		require.Error(t, err)
		assert.ErrorContains(t, err, "state store error")
		assert.NotErrorIs(t, err, ErrStateNotFound)
	})
}
