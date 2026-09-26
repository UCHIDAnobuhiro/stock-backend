// Package redis はRedisクライアントの初期化と設定を提供します。
package redis

import (
	"context"
	"log/slog"
	"net"

	"github.com/redis/go-redis/v9"
)

// Password はログ出力・文字列化・JSONシリアライズ時に値をマスクする機密文字列型です。
// fmt.Stringer / fmt.GoStringer / json.Marshaler / slog.LogValuer を実装しているため、
// 誤って構造体ごとログ出力しても平文パスワードは "***" に置換されます。
// redis.Options への設定時など実値が必要な場合は string(p) で明示的に変換してください。
type Password string

// String は %s / %v などのフォーマット時にパスワードをマスクします。
func (Password) String() string { return "***" }

// GoString は %#v 書式でのマスク出力を提供します。
func (Password) GoString() string { return "***" }

// MarshalJSON は JSON シリアライズ時にパスワードをマスクします。
func (Password) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }

// LogValue は slog による構造化ログ出力時にパスワードをマスクします。
func (Password) LogValue() slog.Value { return slog.StringValue("***") }

// NewRedisClient は渡された接続情報で新しいRedisクライアントを作成します。
// 返却前にPINGコマンドで接続を検証します。
// 設定の読み込み（環境変数 REDIS_HOST / REDIS_PORT / REDIS_PASSWORD）は
// internal/app/config に集約されています。password は空文字を許容します。
func NewRedisClient(host, port, password string) (*redis.Client, error) {
	addr := net.JoinHostPort(host, port)

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	// 接続を検証
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		slog.Error("Redis connection failed", "address", addr, "error", err)
		if closeErr := rdb.Close(); closeErr != nil {
			slog.Warn("Failed to close Redis client after connection failure", "error", closeErr)
		}
		return nil, err
	}

	slog.Info("Redis connection successful", "address", addr)
	return rdb, nil
}
