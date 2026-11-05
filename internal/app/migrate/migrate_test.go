package migrate

import (
	"log/slog"
	"testing"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
)

// TestRun_RejectsUnsupportedCommand は allowedCommands に含まれないサブコマンドを
// Run() が拒否し、終了コード 2 を返すことを検証します。
// OpenSQL を呼ぶ前に弾かれるため、DB なしで実行可能です。
func TestRun_RejectsUnsupportedCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "create (developer-only)", args: []string{"create", "foo", "sql"}},
		{name: "fix (developer-only)", args: []string{"fix"}},
		{name: "unknown command", args: []string{"no-such"}},
	}

	cfg := &config.Config{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Run(cfg, tt.args)
			if got != 2 {
				t.Errorf("Run(%v) = %d, want 2", tt.args, got)
			}
		})
	}
}

func TestRun_DoesNotChangeDefaultLogger(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	logger := slog.New(slog.DiscardHandler)
	slog.SetDefault(logger)

	if got := Run(&config.Config{}, []string{"no-such"}); got != 2 {
		t.Fatalf("Run() = %d, want 2", got)
	}
	if got := slog.Default(); got != logger {
		t.Errorf("slog.Default() = %p, want %p", got, logger)
	}
}

// TestAllowedCommands_ContainsExpectedSet は本バイナリが受け付けるべき
// サブコマンドの集合を固定化します（誤って広げない / 狭めないため）。
func TestAllowedCommands_ContainsExpectedSet(t *testing.T) {
	t.Parallel()

	want := []string{
		"up", "up-by-one", "up-to",
		"down", "down-to",
		"redo", "reset",
		"status", "version",
	}
	if len(allowedCommands) != len(want) {
		t.Errorf("allowedCommands size = %d, want %d", len(allowedCommands), len(want))
	}
	for _, c := range want {
		if _, ok := allowedCommands[c]; !ok {
			t.Errorf("allowedCommands missing %q", c)
		}
	}

	// 開発者ローカル専用コマンドが含まれていないことを検証
	for _, banned := range []string{"create", "fix"} {
		if _, ok := allowedCommands[banned]; ok {
			t.Errorf("allowedCommands must not include %q (developer-only)", banned)
		}
	}
}

func TestRun_ReturnsOneWhenDBConfigInvalid(t *testing.T) {
	t.Parallel()

	// DB_USER 未設定相当の不正な DB Config → OpenSQL の検証で失敗し 1 を返す。
	cfg := &config.Config{DB: infradb.Config{}}
	if got := Run(cfg, nil); got != 1 {
		t.Errorf("Run(nil) = %d, want 1", got)
	}
}
