package jwt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Blacklist はRedisを使ってログアウト済みJWTのjtiを即時失効させます。
// rdbがnilの場合は常に「失効していない」として扱います（グレースフルデグレード）。
type Blacklist struct {
	rdb *redis.Client
}

// NewBlacklist はBlacklistの新しいインスタンスを生成します。
func NewBlacklist(rdb *redis.Client) *Blacklist {
	return &Blacklist{rdb: rdb}
}

func blacklistKey(jti string) string {
	return fmt.Sprintf("jwt:blacklist:%s", jti)
}

// Revoke はjtiをttl付きでブラックリストに登録します。
// ttlにはトークンの残り有効期限を渡すことで、トークンがexpを迎えるのと同時に
// Redis上のエントリも自動的に消え、ブラックリストが際限なく肥大化しないようにします。
// jtiが空、またはttlが0以下（=既に期限切れ）の場合は何もしません。
func (b *Blacklist) Revoke(ctx context.Context, jti string, ttl time.Duration) error {
	if b == nil || b.rdb == nil {
		slog.Warn("jwt blacklist unavailable, logout will not revoke token immediately")
		return nil
	}
	if jti == "" || ttl <= 0 {
		return nil
	}
	return b.rdb.Set(ctx, blacklistKey(jti), "1", ttl).Err()
}

// IsRevoked はjtiがブラックリストに登録されているかを確認します。
// Redis未接続・エラー時は「失効していない」として扱います（フェイルオープン。
// 他のRedis依存コンポーネント（レートリミッター・キャッシュ）と同じグレースフルデグレード方針）。
func (b *Blacklist) IsRevoked(ctx context.Context, jti string) bool {
	if b == nil || b.rdb == nil || jti == "" {
		return false
	}
	exists, err := b.rdb.Exists(ctx, blacklistKey(jti)).Result()
	if err != nil {
		slog.Warn("jwt blacklist check failed, allowing request", "error", err)
		return false
	}
	return exists > 0
}
