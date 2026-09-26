package redis

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestPassword_Masking は Password 型がログ・文字列化・JSON シリアライズのいずれの経路でも
// 平文を露出せず "***" にマスクされることを検証します。
func TestPassword_Masking(t *testing.T) {
	t.Parallel()

	const secret = "super-secret"
	p := Password(secret)

	t.Run("String", func(t *testing.T) {
		t.Parallel()
		if got := p.String(); got != "***" {
			t.Errorf("String() = %q, want %q", got, "***")
		}
	})

	t.Run("GoString", func(t *testing.T) {
		t.Parallel()
		if got := p.GoString(); got != "***" {
			t.Errorf("GoString() = %q, want %q", got, "***")
		}
	})

	t.Run("fmt %v and %s do not leak", func(t *testing.T) {
		t.Parallel()
		for _, verb := range []string{"%v", "%s", "%+v", "%#v"} {
			got := fmt.Sprintf(verb, p)
			if strings.Contains(got, secret) {
				t.Errorf("fmt %q leaked secret: %q", verb, got)
			}
		}
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		t.Parallel()
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(b) != `"***"` {
			t.Errorf("MarshalJSON = %s, want %q", b, `"***"`)
		}
	})

	t.Run("slog structured logging does not leak", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		logger.Info("connecting", "password", p)
		if strings.Contains(buf.String(), secret) {
			t.Errorf("slog leaked secret: %s", buf.String())
		}
	})

	t.Run("explicit string conversion still exposes value", func(t *testing.T) {
		t.Parallel()
		// redis.Options.Password への設定など実値が必要な場面では明示的変換で取得できる
		if string(p) != secret {
			t.Errorf("string(p) = %q, want %q", string(p), secret)
		}
	})
}

func TestNewRedisClient_Ping(t *testing.T) {
	server := miniredis.RunT(t)
	host, port, ok := strings.Cut(server.Addr(), ":")
	if !ok {
		t.Fatalf("invalid Redis address: %q", server.Addr())
	}

	client, err := NewRedisClient(host, port, "")
	if err != nil || client == nil {
		t.Fatalf("healthy Redis: client = %v, err = %v", client, err)
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Errorf("returned client cannot PING Redis: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("close client: %v", err)
	}

	server.SetError("LOADING Redis is loading the dataset in memory")
	client, err = NewRedisClient(host, port, "")
	if err == nil || client != nil {
		t.Errorf("unavailable Redis: client = %v, err = %v; want nil client and error", client, err)
	}

	server.SetError("")
	client, err = NewRedisClient(host, port, "")
	if err != nil || client == nil {
		t.Fatalf("recovered Redis: client = %v, err = %v", client, err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("close recovered client: %v", err)
	}
}
