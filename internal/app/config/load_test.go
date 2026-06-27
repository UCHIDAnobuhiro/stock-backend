package config

import (
	"testing"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// validSecret は最低長(minSecretLength=32)を満たすテスト用シークレット。
const validSecret = "0123456789abcdef0123456789abcdef" // 32 バイト

// clearServerEnv は設定検証に関わる環境変数をすべて空にし、テストを決定的にする。
func clearServerEnv(t *testing.T) {
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

func TestLoadAPI(t *testing.T) {
	t.Run("JWT_SECRET 未設定はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret)
		if _, err := LoadAPI(); err == nil {
			t.Fatal("expected error when JWT_SECRET is missing, got nil")
		}
	})

	t.Run("JWT_SECRET が短すぎる場合はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret[:minSecretLength-1])
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret)
		if _, err := LoadAPI(); err == nil {
			t.Fatalf("expected error when JWT_SECRET is shorter than %d bytes, got nil", minSecretLength)
		}
	})

	t.Run("PASSWORD_PEPPER 未設定はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret)
		if _, err := LoadAPI(); err == nil {
			t.Fatal("expected error when PASSWORD_PEPPER is missing, got nil")
		}
	})

	t.Run("PASSWORD_PEPPER が短すぎる場合はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret)
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret[:minSecretLength-1])
		if _, err := LoadAPI(); err == nil {
			t.Fatalf("expected error when PASSWORD_PEPPER is shorter than %d bytes, got nil", minSecretLength)
		}
	})

	t.Run("ちょうど最低長で成功", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret[:minSecretLength])
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret[:minSecretLength])
		if _, err := LoadAPI(); err != nil {
			t.Fatalf("unexpected error for %d-byte secrets: %v", minSecretLength, err)
		}
	})

	t.Run("必須のみ設定で成功・デフォルト適用", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret)
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret)

		cfg, err := LoadAPI()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.OAuth != nil {
			t.Errorf("oauth should be nil when no provider configured, got %+v", cfg.OAuth)
		}
		if cfg.Server.SecureCookie {
			t.Error("secureCookie should default to false without APP_ENV=production")
		}
		if len(cfg.Server.CORSOrigins) != 1 || cfg.Server.CORSOrigins[0] != defaultCORSOrigin {
			t.Errorf("corsOrigins should default to %s, got %v", defaultCORSOrigin, cfg.Server.CORSOrigins)
		}
	})

	t.Run("APP_ENV=production で secureCookie が true", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret)
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret)
		t.Setenv("APP_ENV", "production")

		cfg, err := LoadAPI()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Server.SecureCookie {
			t.Error("secureCookie should be true when APP_ENV=production")
		}
	})

	t.Run("不正な COOKIE_SECURE は Warnings に記録しデフォルト動作", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv(jwt.EnvKeyJWTSecret, validSecret)
		t.Setenv(auth.EnvKeyPasswordPepper, validSecret)
		t.Setenv("COOKIE_SECURE", "notabool")

		cfg, err := LoadAPI()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Warnings) == 0 {
			t.Error("expected a warning for invalid COOKIE_SECURE")
		}
		if cfg.Server.SecureCookie {
			t.Error("secureCookie should fall back to false")
		}
	})
}

func TestReadOAuth(t *testing.T) {
	t.Run("プロバイダ未設定は無効(nil)", func(t *testing.T) {
		clearServerEnv(t)
		cfg, err := readOAuth()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg != nil {
			t.Errorf("expected nil config when OAuth disabled, got %+v", cfg)
		}
	})

	t.Run("frontend URL 欠落はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GOOGLE_CLIENT_ID", "gid")
		if _, err := readOAuth(); err == nil {
			t.Fatal("expected error when OAUTH_FRONTEND_REDIRECT_URL is missing")
		}
	})

	t.Run("Google secret 欠落はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GOOGLE_CLIENT_ID", "gid")
		t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://app.example.com")
		t.Setenv("GOOGLE_REDIRECT_URL", "https://api.example.com/cb")
		if _, err := readOAuth(); err == nil {
			t.Fatal("expected error when GOOGLE_CLIENT_SECRET is missing")
		}
	})

	t.Run("Google redirect 欠落はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GOOGLE_CLIENT_ID", "gid")
		t.Setenv("GOOGLE_CLIENT_SECRET", "gsec")
		t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://app.example.com")
		if _, err := readOAuth(); err == nil {
			t.Fatal("expected error when GOOGLE_REDIRECT_URL is missing")
		}
	})

	t.Run("Google 完全設定で google のみ生成", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GOOGLE_CLIENT_ID", "gid")
		t.Setenv("GOOGLE_CLIENT_SECRET", "gsec")
		t.Setenv("GOOGLE_REDIRECT_URL", "https://api.example.com/cb")
		t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://app.example.com")

		cfg, err := readOAuth()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil || cfg.Google == nil {
			t.Fatalf("expected google config, got %+v", cfg)
		}
		if cfg.GitHub != nil {
			t.Error("github should be nil when not configured")
		}
	})

	t.Run("GitHub secret 欠落はエラー", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GITHUB_CLIENT_ID", "hid")
		t.Setenv("GITHUB_REDIRECT_URL", "https://api.example.com/cb")
		t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://app.example.com")
		if _, err := readOAuth(); err == nil {
			t.Fatal("expected error when GITHUB_CLIENT_SECRET is missing")
		}
	})

	t.Run("両プロバイダ完全設定で両方生成", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("GOOGLE_CLIENT_ID", "gid")
		t.Setenv("GOOGLE_CLIENT_SECRET", "gsec")
		t.Setenv("GOOGLE_REDIRECT_URL", "https://api.example.com/google/cb")
		t.Setenv("GITHUB_CLIENT_ID", "hid")
		t.Setenv("GITHUB_CLIENT_SECRET", "hsec")
		t.Setenv("GITHUB_REDIRECT_URL", "https://api.example.com/github/cb")
		t.Setenv("OAUTH_FRONTEND_REDIRECT_URL", "https://app.example.com")

		cfg, err := readOAuth()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil || cfg.Google == nil || cfg.GitHub == nil {
			t.Fatalf("expected both providers, got %+v", cfg)
		}
	})
}

func TestLoadBatch(t *testing.T) {
	t.Run("未設定はデフォルト値を適用", func(t *testing.T) {
		for _, k := range []string{
			"INGEST_TIMEOUT_HOURS", "INGEST_MAX_FAILURE_RATE",
			"LOGO_INGEST_TIMEOUT_HOURS", "LOGO_INGEST_MAX_FAILURE_RATE",
		} {
			t.Setenv(k, "")
		}
		cfg, err := LoadBatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Batch.CandlesTimeoutHours != defaultIngestTimeoutHours {
			t.Errorf("CandlesTimeoutHours = %d, want %d", cfg.Batch.CandlesTimeoutHours, defaultIngestTimeoutHours)
		}
		if cfg.Batch.CandlesMaxFailureRate != defaultMaxFailureRate {
			t.Errorf("CandlesMaxFailureRate = %v, want %v", cfg.Batch.CandlesMaxFailureRate, defaultMaxFailureRate)
		}
	})

	t.Run("有効な値を読み込む", func(t *testing.T) {
		t.Setenv("INGEST_TIMEOUT_HOURS", "5")
		t.Setenv("INGEST_MAX_FAILURE_RATE", "0.5")
		t.Setenv("LOGO_INGEST_TIMEOUT_HOURS", "2")
		t.Setenv("LOGO_INGEST_MAX_FAILURE_RATE", "0.1")

		cfg, err := LoadBatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Batch.CandlesTimeoutHours != 5 || cfg.Batch.CandlesMaxFailureRate != 0.5 {
			t.Errorf("unexpected candles batch config: %+v", cfg.Batch)
		}
		if cfg.Batch.LogoTimeoutHours != 2 || cfg.Batch.LogoMaxFailureRate != 0.1 {
			t.Errorf("unexpected logo batch config: %+v", cfg.Batch)
		}
	})

	t.Run("不正な失敗率は Warnings に記録しデフォルト", func(t *testing.T) {
		t.Setenv("INGEST_TIMEOUT_HOURS", "")
		t.Setenv("INGEST_MAX_FAILURE_RATE", "2.0") // 範囲外
		t.Setenv("LOGO_INGEST_TIMEOUT_HOURS", "")
		t.Setenv("LOGO_INGEST_MAX_FAILURE_RATE", "")

		cfg, err := LoadBatch()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Warnings) == 0 {
			t.Error("expected a warning for invalid INGEST_MAX_FAILURE_RATE")
		}
		if cfg.Batch.CandlesMaxFailureRate != defaultMaxFailureRate {
			t.Errorf("CandlesMaxFailureRate should fall back to default, got %v", cfg.Batch.CandlesMaxFailureRate)
		}
	})
}
