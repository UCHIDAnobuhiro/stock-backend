package jwt

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
)

// TestBlacklist_Revoke はjtiがttl付きでRedisに登録されることを検証します。
func TestBlacklist_Revoke(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	const jti = "some-jti"
	ttl := 30 * time.Minute
	mock.ExpectSet(blacklistKey(jti), "1", ttl).SetVal("OK")

	bl := NewBlacklist(rdb)
	if err := bl.Revoke(context.Background(), jti, ttl); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestBlacklist_Revoke_NoopCases はjti空文字・ttl<=0の場合にRedisを呼ばないことを検証します。
func TestBlacklist_Revoke_NoopCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		jti  string
		ttl  time.Duration
	}{
		{"empty jti", "", time.Hour},
		{"zero ttl", "some-jti", 0},
		{"negative ttl", "some-jti", -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb, mock := redismock.NewClientMock()
			t.Cleanup(func() { _ = rdb.Close() })

			bl := NewBlacklist(rdb)
			if err := bl.Revoke(context.Background(), tt.jti, tt.ttl); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("expected no Redis calls, but: %v", err)
			}
		})
	}
}

// TestBlacklist_Revoke_NilRedis はRedis未接続時にエラーを返さず何もしないことを検証します（グレースフルデグレード）。
func TestBlacklist_Revoke_NilRedis(t *testing.T) {
	t.Parallel()

	bl := NewBlacklist(nil)
	if err := bl.Revoke(context.Background(), "some-jti", time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBlacklist_IsRevoked はブラックリスト登録済み・未登録の両方を正しく判定することを検証します。
func TestBlacklist_IsRevoked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exists   int64
		expected bool
	}{
		{"revoked", 1, true},
		{"not revoked", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb, mock := redismock.NewClientMock()
			t.Cleanup(func() { _ = rdb.Close() })

			const jti = "some-jti"
			mock.ExpectExists(blacklistKey(jti)).SetVal(tt.exists)

			bl := NewBlacklist(rdb)
			if got := bl.IsRevoked(context.Background(), jti); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

// TestBlacklist_IsRevoked_NilRedis はRedis未接続時に「失効していない」として扱う（フェイルオープン）ことを検証します。
func TestBlacklist_IsRevoked_NilRedis(t *testing.T) {
	t.Parallel()

	bl := NewBlacklist(nil)
	if bl.IsRevoked(context.Background(), "some-jti") {
		t.Error("expected IsRevoked to return false when Redis is unavailable")
	}
}

// TestBlacklist_IsRevoked_EmptyJTI は空文字のjtiに対してRedisを呼ばず false を返すことを検証します。
func TestBlacklist_IsRevoked_EmptyJTI(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	bl := NewBlacklist(rdb)
	if bl.IsRevoked(context.Background(), "") {
		t.Error("expected false for empty jti")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no Redis calls, but: %v", err)
	}
}

// TestBlacklist_IsRevoked_RedisError はRedisエラー時に「失効していない」として扱う（フェイルオープン）ことを検証します。
func TestBlacklist_IsRevoked_RedisError(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	const jti = "some-jti"
	mock.ExpectExists(blacklistKey(jti)).SetErr(context.DeadlineExceeded)

	bl := NewBlacklist(rdb)
	if bl.IsRevoked(context.Background(), jti) {
		t.Error("expected false when Redis returns an error")
	}
}

// TestBlacklist_NilReceiver はBlacklistがnilポインタでも安全に呼び出せることを検証します
// （AuthRequiredにblacklist未設定で渡された場合の防御的実装）。
func TestBlacklist_NilReceiver(t *testing.T) {
	t.Parallel()

	var bl *Blacklist
	if err := bl.Revoke(context.Background(), "some-jti", time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bl.IsRevoked(context.Background(), "some-jti") {
		t.Error("expected false for nil Blacklist")
	}
}
