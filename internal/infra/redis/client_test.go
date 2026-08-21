package redis

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"testing"
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
