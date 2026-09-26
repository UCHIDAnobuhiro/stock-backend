package main

import (
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// clearEnv は設定検証に関わる環境変数をすべて空にし、テストを決定的にする。
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		jwt.EnvKeyJWTSecret,
		auth.EnvKeyPasswordPepper,
		"COOKIE_SECURE",
		"COOKIE_DOMAIN",
		"APP_ENV",
		"CORS_ALLOWED_ORIGINS",
		"GOOGLE_CLIENT_ID",
		"GOOGLE_CLIENT_SECRET",
		"GOOGLE_REDIRECT_URL",
		"GITHUB_CLIENT_ID",
		"GITHUB_CLIENT_SECRET",
		"GITHUB_REDIRECT_URL",
		"OAUTH_FRONTEND_REDIRECT_URL",
	} {
		t.Setenv(k, "")
	}
}

func TestRun_ReturnsTwoWhenConfigInvalid(t *testing.T) {
	clearEnv(t) // JWT_SECRET 未設定 → loadServerConfig が失敗する

	if got := run(); got != 2 {
		t.Errorf("run() = %d, want 2", got)
	}
}

func TestRun_ReturnsOneWhenDBConfigInvalid(t *testing.T) {
	clearEnv(t)
	// 最低長(32 バイト)を満たす値で config 検証を通し、DB 設定不備で失敗させる
	t.Setenv(jwt.EnvKeyJWTSecret, "0123456789abcdef0123456789abcdef")
	t.Setenv(auth.EnvKeyPasswordPepper, "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_USER", "")

	if got := run(); got != 1 {
		t.Errorf("run() = %d, want 1", got)
	}
}

func TestRun_RedisUnavailableStopsStartup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		oauth bool
	}{
		{name: "OAuth disabled"},
		{name: "OAuth enabled", oauth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(jwt.EnvKeyJWTSecret, "0123456789abcdef0123456789abcdef")
			t.Setenv(auth.EnvKeyPasswordPepper, "0123456789abcdef0123456789abcdef")
			if tc.oauth {
				t.Setenv("GOOGLE_CLIENT_ID", "client-id")
				t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
				t.Setenv("GOOGLE_REDIRECT_URL", "https://example.com/callback")
				t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://example.com")
			}

			server := miniredis.RunT(t)
			server.SetError("LOADING Redis is loading the dataset in memory")
			host, port, ok := strings.Cut(server.Addr(), ":")
			if !ok {
				t.Fatalf("invalid Redis address: %q", server.Addr())
			}
			t.Setenv("REDIS_HOST", host)
			t.Setenv("REDIS_PORT", port)
			t.Setenv("REDIS_PASSWORD", "")

			logFile, err := os.CreateTemp(t.TempDir(), "api-startup-*.log")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := logFile.Close(); err != nil {
					t.Errorf("close log file: %v", err)
				}
			}()
			stdout := os.Stdout
			defaultLogger := slog.Default()
			os.Stdout = logFile
			defer func() {
				os.Stdout = stdout
				slog.SetDefault(defaultLogger)
			}()

			openedDB := false
			got := runWithDBOpener(func(db.Config) (*sql.DB, error) {
				openedDB = true
				return sql.Open("pgx", "postgres://localhost/test")
			})
			if !openedDB {
				t.Error("DB opener was not called")
			}
			if got != 1 {
				t.Errorf("runWithDBOpener() = %d, want 1", got)
			}
			output, err := os.ReadFile(logFile.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(output), "Redis startup failed") {
				t.Errorf("Redis failure did not stop startup: %s", output)
			}
			if strings.Contains(string(output), "Starting server") {
				t.Errorf("HTTP server started despite Redis failure: %s", output)
			}
		})
	}
}
