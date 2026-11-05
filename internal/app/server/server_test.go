package server

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/di"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// newTestConfig は config.LoadAPI の検証を通過した状態に相当する設定を返す。
// DB は未設定（ゼロ値）のため、DB 接続まで到達すると OpenSQL の検証で失敗する。
func newTestConfig(redisAddr string) *config.Config {
	host, port, _ := strings.Cut(redisAddr, ":")
	return &config.Config{
		Redis: config.RedisConfig{Host: host, Port: port},
		Server: config.ServerConfig{
			JWTSecret:      testSecret,
			PasswordPepper: testSecret,
		},
	}
}

// captureLog は slog のデフォルトロガーをバッファに差し替え、テスト終了時に元へ戻す。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func TestRun_ReturnsOneWhenDBConfigInvalid(t *testing.T) {
	// Redis は DB より先に接続するため、正常な Redis を用意して DB 設定不備まで到達させる
	server := miniredis.RunT(t)
	output := captureLog(t)

	if got := Run(newTestConfig(server.Addr())); got != 1 {
		t.Errorf("Run() = %d, want 1", got)
	}
	if !strings.Contains(output.String(), "DB open failed") {
		t.Errorf("startup did not fail at DB open: %s", output)
	}
}

func TestRun_RedisUnavailableStopsStartup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		oauth *di.OAuthConfig
	}{
		{name: "OAuth disabled"},
		{name: "OAuth enabled", oauth: &di.OAuthConfig{
			FrontendURL: "https://example.com",
			Google: &di.ProviderCredentials{
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				RedirectURL:  "https://example.com/callback",
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 起動停止が退行しても、開発環境のADCでVision初期化が成功してサーバーが待機しないようにする。
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing-adc.json"))

			server := miniredis.RunT(t)
			server.SetError("LOADING Redis is loading the dataset in memory")
			output := captureLog(t)

			cfg := newTestConfig(server.Addr())
			cfg.OAuth = tc.oauth
			if got := Run(cfg); got != 1 {
				t.Errorf("Run() = %d, want 1", got)
			}

			log := output.String()
			if !strings.Contains(log, "Redis startup failed") {
				t.Errorf("Redis failure did not stop startup: %s", log)
			}
			for _, unexpected := range []string{"DB open failed", "failed to create vision client", "Starting server"} {
				if strings.Contains(log, unexpected) {
					t.Errorf("startup proceeded after Redis failure (%q logged): %s", unexpected, log)
				}
			}
		})
	}
}
