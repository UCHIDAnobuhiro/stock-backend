package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestHashedEmail_LogValue は HashedEmail 型が slog 出力時に期待通りハッシュ化されることを検証します。
func TestHashedEmail_LogValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     HashedEmail
		wantEmpty bool
	}{
		{name: "通常のメールアドレス", input: "user@example.com"},
		{name: "別のメールアドレス", input: "other@example.com"},
		{name: "空文字列", input: "", wantEmpty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.input.LogValue().String()
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("LogValue() = %q, want empty", got)
				}
				return
			}
			if len(got) != hashedEmailLen {
				t.Errorf("LogValue() length = %d, want %d (got %q)", len(got), hashedEmailLen, got)
			}
			if strings.Contains(got, string(tt.input)) {
				t.Errorf("LogValue() leaked plaintext: %q", got)
			}
		})
	}
}

// TestHashedEmail_CaseInsensitive は大文字小文字の違いを正規化して同一ハッシュを返すことを検証します。
// レート制限キー（auth_handler.go 側で strings.ToLower 済み）との突き合わせを可能にするため。
func TestHashedEmail_CaseInsensitive(t *testing.T) {
	t.Parallel()

	a := HashedEmail("User@Example.com").LogValue().String()
	b := HashedEmail("user@example.com").LogValue().String()
	if a != b {
		t.Errorf("case mismatch: %q != %q", a, b)
	}
}

// TestHashedEmail_TrimSpace は前後の空白の有無で同一ハッシュを返すことを検証します。
// usecase 側の正規化（trim + 小文字化）とログ相関を一致させるため。
func TestHashedEmail_TrimSpace(t *testing.T) {
	t.Parallel()

	a := HashedEmail("  User@Example.com  ").LogValue().String()
	b := HashedEmail("user@example.com").LogValue().String()
	if a != b {
		t.Errorf("whitespace/case mismatch: %q != %q", a, b)
	}
}

// TestHashedEmail_Deterministic は同一入力が常に同一ハッシュを返すことを検証します。
// 連続失敗・レート制限ログの相関解析が成立する前提条件。
func TestHashedEmail_Deterministic(t *testing.T) {
	t.Parallel()

	a := HashedEmail("user@example.com").LogValue().String()
	b := HashedEmail("user@example.com").LogValue().String()
	if a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}
}

// TestHashedEmail_Distinct は異なる入力が異なるハッシュを返すことを検証します。
func TestHashedEmail_Distinct(t *testing.T) {
	t.Parallel()

	a := HashedEmail("user1@example.com").LogValue().String()
	b := HashedEmail("user2@example.com").LogValue().String()
	if a == b {
		t.Errorf("collision: both produced %q", a)
	}
}

// TestHashedEmail_SlogIntegration は実際に slog 経由で出力した際にハッシュが
// JSON ハンドラの出力に現れ、平文が含まれないことを検証します。
func TestHashedEmail_SlogIntegration(t *testing.T) {
	t.Parallel()

	const email = "leak@example.com"

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("test", "email_hash", HashedEmail(email))

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("failed to parse log JSON: %v", err)
	}

	hash, ok := got["email_hash"].(string)
	if !ok {
		t.Fatalf("email_hash field missing or not string: %v", got["email_hash"])
	}
	if len(hash) != hashedEmailLen {
		t.Errorf("hash length = %d, want %d", len(hash), hashedEmailLen)
	}
	if strings.Contains(buf.String(), email) {
		t.Errorf("log output leaked plaintext email: %s", buf.String())
	}
}
