package main

import (
	"testing"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// clearEnv は設定検証に関わる環境変数をすべて空にし、テストを決定的にする。
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		jwt.EnvKeyJWTSecret,
		auth.EnvKeyPasswordPepper,
		"COOKIE_SECURE",
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
