package auth_test

import (
	"testing"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
)

// TestNormalizeEmail は小文字化・前後空白除去が行われることを検証します。
func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "大文字を小文字化", input: "User@Example.com", want: "user@example.com"},
		{name: "前後の空白を除去", input: "  user@example.com  ", want: "user@example.com"},
		{name: "大文字と空白の混在", input: "\tUser@Example.COM\n", want: "user@example.com"},
		{name: "正規化済みはそのまま", input: "user@example.com", want: "user@example.com"},
		{name: "空白のみは空文字列", input: "   ", want: ""},
		{name: "空文字列", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := auth.NormalizeEmail(tt.input); got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
