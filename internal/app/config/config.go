// Package config はアプリケーション全体の環境変数読み込みを一箇所に集約します。
//
// os.Getenv の呼び出しはこのパッケージ内に閉じ込め、他のパッケージは
// 構築済みの設定値を注入される形にします。エントリポイントごとに必要な
// 環境変数と必須項目が異なるため、LoadAPI / LoadBatch / LoadMigrate に
// 分割しています。
//
// 各 Load は logger を生成しません（副作用を持ちません）。返却される *Config は
// 致命的エラー時でも Log を best-effort で埋めて非 nil で返し、非致命的な不正値は
// Warnings に蓄積します。呼び出し側は cfg.Log でロガーを構成し、cfg.Warnings を
// slog.Warn で出力したうえで、err を確認してください。
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/di"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/twelvedata"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

const (
	// defaultCORSOrigin は CORS_ALLOWED_ORIGINS 未設定時のフォールバック。
	defaultCORSOrigin = "http://localhost:3000"
	// defaultIngestTimeoutHours は *_TIMEOUT_HOURS のデフォルト値。
	defaultIngestTimeoutHours = 3
	// defaultMaxFailureRate は *_MAX_FAILURE_RATE のデフォルト値。
	defaultMaxFailureRate = 0.2
	// minSecretLength は JWT_SECRET / PASSWORD_PEPPER に要求する最低バイト数。
	// HS256 署名鍵の総当たり偽造を防ぐため 32 バイト以上を必須とする。
	minSecretLength = 32
)

// Config はアプリケーション全体の設定を保持します。
// 使用するエントリポイントによって、埋められるフィールドのグループが異なります。
type Config struct {
	Log        LogConfig         // 全エントリポイント共通
	DB         db.Config         // API / batch / migrate
	Redis      RedisConfig       // API / batch
	Server     ServerConfig      // API のみ
	OAuth      *di.OAuthConfig   // API のみ（OAuth 無効なら nil）
	TwelveData twelvedata.Config // batch のみ
	Batch      BatchConfig       // batch のみ
	Warnings   []string          // 非致命的な不正値（呼び出し側で slog.Warn する）
}

// LogConfig はロガー構成に必要な設定です。
type LogConfig struct {
	Level   slog.Level
	UseJSON bool
}

// RedisConfig は Redis 接続設定です。
type RedisConfig struct {
	Host     string
	Port     string
	Password string
}

// ServerConfig は API サーバー固有の検証済み設定です。
type ServerConfig struct {
	JWTSecret      string
	PasswordPepper string
	SecureCookie   bool
	CORSOrigins    []string
	GCPProjectID   string // GOOGLE_CLOUD_PROJECT。未設定可（トレース相関に使用）
	// TrustedProxyHops は X-Forwarded-For を信頼するリバースプロキシの段数。
	// middleware.RealIP に渡す。0（デフォルト）なら XFF を一切信頼しない。
	TrustedProxyHops int
}

// BatchConfig はバッチ実行のタイムアウト・失敗率しきい値です。
type BatchConfig struct {
	CandlesTimeoutHours   int
	CandlesMaxFailureRate float64
	LogoTimeoutHours      int
	LogoMaxFailureRate    float64
}

// LoadAPI は API サーバー用の設定を読み込み検証します。
// 必須項目（JWT_SECRET / PASSWORD_PEPPER）の欠落や OAuth 設定の不整合があれば
// エラーを返します。DB の検証は接続時（db.OpenSQL）に行うため、ここでは行いません。
func LoadAPI() (*Config, error) {
	cfg := &Config{}
	cfg.Log = readLog(&cfg.Warnings)
	cfg.DB = readDB(&cfg.Warnings)
	cfg.Redis = readRedis()

	server, err := readServer(&cfg.Warnings)
	if err != nil {
		return cfg, err
	}
	cfg.Server = server

	oauth, err := readOAuth()
	if err != nil {
		return cfg, err
	}
	cfg.OAuth = oauth

	return cfg, nil
}

// LoadBatch はバッチ実行用の設定を読み込みます。
// バッチには必須の環境変数がないため、現状エラーは返しません（将来の拡張のため戻り値は維持）。
func LoadBatch() (*Config, error) {
	cfg := &Config{}
	cfg.Log = readLog(&cfg.Warnings)
	cfg.DB = readDB(&cfg.Warnings)
	cfg.Redis = readRedis()
	cfg.TwelveData = readTwelveData()
	cfg.Batch = readBatch(&cfg.Warnings)
	return cfg, nil
}

// LoadMigrate はマイグレーション実行用の設定を読み込みます。
func LoadMigrate() (*Config, error) {
	cfg := &Config{}
	cfg.Log = readLog(&cfg.Warnings)
	cfg.DB = readDB(&cfg.Warnings)
	return cfg, nil
}

// readLog は LOG_LEVEL / LOG_FORMAT / APP_ENV からロガー設定を組み立てます。
func readLog(warn *[]string) LogConfig {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "DEBUG" {
		level = slog.LevelDebug
	}
	rawFormat := os.Getenv("LOG_FORMAT")
	useJSON, ok := ParseLogFormat(rawFormat, os.Getenv("APP_ENV"))
	if !ok {
		*warn = append(*warn, fmt.Sprintf("invalid LOG_FORMAT value %q, using default", rawFormat))
	}
	return LogConfig{Level: level, UseJSON: useJSON}
}

// readDB は DB_* 環境変数からデータベース設定を組み立てます。
// 必須項目の検証は接続時（Config.Validate）に行います。
// コネクションプール関連の数値・duration は未設定ならゼロ値のままにし、
// db パッケージ側のデフォルト（Default* 定数）にフォールバックさせます。不正値は警告を蓄積します。
func readDB(warn *[]string) db.Config {
	return db.Config{
		User:            os.Getenv("DB_USER"),
		Password:        db.Password(os.Getenv("DB_PASSWORD")),
		Name:            os.Getenv("DB_NAME"),
		Host:            os.Getenv("DB_HOST"),
		Port:            os.Getenv("DB_PORT"),
		InstanceName:    os.Getenv("INSTANCE_CONNECTION_NAME"),
		SSLMode:         os.Getenv("DB_SSLMODE"),
		MaxOpenConns:    readInt("DB_MAX_OPEN_CONNS", warn),
		MaxIdleConns:    readInt("DB_MAX_IDLE_CONNS", warn),
		ConnMaxLifetime: readDuration("DB_CONN_MAX_LIFETIME", warn),
	}
}

// readRedis は REDIS_* 環境変数から Redis 接続設定を組み立てます。
func readRedis() RedisConfig {
	return RedisConfig{
		Host:     os.Getenv("REDIS_HOST"),
		Port:     os.Getenv("REDIS_PORT"),
		Password: os.Getenv("REDIS_PASSWORD"),
	}
}

// readTwelveData は TWELVE_DATA_* 環境変数から TwelveData クライアント設定を組み立てます。
func readTwelveData() twelvedata.Config {
	return twelvedata.NewConfig(
		os.Getenv("TWELVE_DATA_API_KEY"),
		os.Getenv("TWELVE_DATA_BASE_URL"),
	)
}

// readServer は API サーバー固有の環境変数を読み込み検証します。
func readServer(warn *[]string) (ServerConfig, error) {
	jwtSecret := os.Getenv(jwt.EnvKeyJWTSecret)
	if jwtSecret == "" {
		return ServerConfig{}, fmt.Errorf("%s is required", jwt.EnvKeyJWTSecret)
	}
	if len(jwtSecret) < minSecretLength {
		return ServerConfig{}, fmt.Errorf("%s must be at least %d bytes", jwt.EnvKeyJWTSecret, minSecretLength)
	}

	passwordPepper := os.Getenv(auth.EnvKeyPasswordPepper)
	if passwordPepper == "" {
		return ServerConfig{}, fmt.Errorf("%s is required", auth.EnvKeyPasswordPepper)
	}
	if len(passwordPepper) < minSecretLength {
		return ServerConfig{}, fmt.Errorf("%s must be at least %d bytes", auth.EnvKeyPasswordPepper, minSecretLength)
	}

	// COOKIE_SECURE を優先し、未設定なら APP_ENV=production をフォールバックとして使用
	cookieSecureRaw := os.Getenv("COOKIE_SECURE")
	defaultSecure := os.Getenv("APP_ENV") == "production"
	secureCookie, ok := ParseBoolString(cookieSecureRaw, defaultSecure)
	if !ok {
		*warn = append(*warn, fmt.Sprintf("invalid COOKIE_SECURE value %q, falling back to default %v", cookieSecureRaw, secureCookie))
	}

	// CORS許可オリジン（デフォルト: http://localhost:3000）
	corsOrigins := ParseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if corsOrigins == nil {
		corsOrigins = []string{defaultCORSOrigin}
	}

	trustedProxyHops := readNonNegativeInt("TRUSTED_PROXY_HOPS", warn)

	return ServerConfig{
		JWTSecret:        jwtSecret,
		PasswordPepper:   passwordPepper,
		SecureCookie:     secureCookie,
		CORSOrigins:      corsOrigins,
		GCPProjectID:     os.Getenv("GOOGLE_CLOUD_PROJECT"),
		TrustedProxyHops: trustedProxyHops,
	}, nil
}

// readOAuth は OAuth 関連の環境変数を検証します。
// GOOGLE_CLIENT_ID / GITHUB_CLIENT_ID のいずれも未設定なら OAuth 無効として nil を返します。
func readOAuth() (*di.OAuthConfig, error) {
	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	githubClientID := os.Getenv("GITHUB_CLIENT_ID")
	if googleClientID == "" && githubClientID == "" {
		return nil, nil
	}

	frontendURL := os.Getenv("OAUTH_FRONTEND_REDIRECT_URL")
	if frontendURL == "" {
		return nil, fmt.Errorf("OAUTH_FRONTEND_REDIRECT_URL is required when OAuth is enabled")
	}

	cfg := &di.OAuthConfig{FrontendURL: frontendURL}

	if googleClientID != "" {
		secret := os.Getenv("GOOGLE_CLIENT_SECRET")
		if secret == "" {
			return nil, fmt.Errorf("GOOGLE_CLIENT_SECRET is required when GOOGLE_CLIENT_ID is set")
		}
		redirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
		if redirectURL == "" {
			return nil, fmt.Errorf("GOOGLE_REDIRECT_URL is required when GOOGLE_CLIENT_ID is set")
		}
		cfg.Google = &di.ProviderCredentials{ClientID: googleClientID, ClientSecret: secret, RedirectURL: redirectURL}
	}

	if githubClientID != "" {
		secret := os.Getenv("GITHUB_CLIENT_SECRET")
		if secret == "" {
			return nil, fmt.Errorf("GITHUB_CLIENT_SECRET is required when GITHUB_CLIENT_ID is set")
		}
		redirectURL := os.Getenv("GITHUB_REDIRECT_URL")
		if redirectURL == "" {
			return nil, fmt.Errorf("GITHUB_REDIRECT_URL is required when GITHUB_CLIENT_ID is set")
		}
		cfg.GitHub = &di.ProviderCredentials{ClientID: githubClientID, ClientSecret: secret, RedirectURL: redirectURL}
	}

	return cfg, nil
}

// readBatch はバッチ実行のタイムアウト・失敗率しきい値を読み込みます。
func readBatch(warn *[]string) BatchConfig {
	return BatchConfig{
		CandlesTimeoutHours:   readTimeoutHours("INGEST_TIMEOUT_HOURS", defaultIngestTimeoutHours),
		CandlesMaxFailureRate: readMaxFailureRate("INGEST_MAX_FAILURE_RATE", defaultMaxFailureRate, warn),
		LogoTimeoutHours:      readTimeoutHours("LOGO_INGEST_TIMEOUT_HOURS", defaultIngestTimeoutHours),
		LogoMaxFailureRate:    readMaxFailureRate("LOGO_INGEST_MAX_FAILURE_RATE", defaultMaxFailureRate, warn),
	}
}

// readTimeoutHours は env のタイムアウト時間（正の整数）を読み取ります。未設定・不正時は def を返します。
func readTimeoutHours(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// readMaxFailureRate は env の失敗率しきい値（[0,1]）を読み取ります。不正時は警告を蓄積して def を返します。
func readMaxFailureRate(key string, def float64, warn *[]string) float64 {
	if v := os.Getenv(key); v != "" {
		if r, err := strconv.ParseFloat(v, 64); err == nil && r >= 0 && r <= 1 {
			return r
		}
		*warn = append(*warn, fmt.Sprintf("invalid max failure rate for %s=%q, using default %v", key, v, def))
	}
	return def
}

// readInt は env の正の整数を読み取ります。未設定なら 0 を返し、呼び出し側（db パッケージ）の
// デフォルトにフォールバックさせます。不正値（非整数・0 以下）の場合は警告を蓄積して 0 を返します。
func readInt(key string, warn *[]string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		*warn = append(*warn, fmt.Sprintf("invalid int for %s=%q, using default", key, v))
		return 0
	}
	return n
}

// readNonNegativeInt は env の 0 以上の整数を読み取ります。未設定なら 0 を返します
// （警告なし。TRUSTED_PROXY_HOPS 等、0 が正当なデフォルト値である項目向け）。
// readInt は n<=0 を不正値として扱うため、0 を許容する本ヘルパーを別途用意しています。
// 不正値（非整数・負数）の場合は警告を蓄積して 0 を返します。
func readNonNegativeInt(key string, warn *[]string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		*warn = append(*warn, fmt.Sprintf("invalid non-negative int for %s=%q, using default 0", key, v))
		return 0
	}
	return n
}

// readDuration は env の duration（time.ParseDuration 形式、例 "5m"）を読み取ります。
// 未設定なら 0 を返し、呼び出し側のデフォルトにフォールバックさせます。
// 不正値（解釈不能・0 以下）の場合は警告を蓄積して 0 を返します。
func readDuration(key string, warn *[]string) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		*warn = append(*warn, fmt.Sprintf("invalid duration for %s=%q, using default", key, v))
		return 0
	}
	return d
}

// ParseCORSOrigins は CORS_ALLOWED_ORIGINS env の生文字列を、カンマ区切りで
// trim して空要素を除いたスライスに変換する。raw が空なら nil を返し、
// 呼び出し側にデフォルト適用を委ねる。
func ParseCORSOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	if len(origins) == 0 {
		return nil
	}
	return origins
}

// ParseBoolString は raw を bool として解釈する。
//   - raw が空文字の場合は (fallback, true) を返す（未設定は正常系扱い）。
//   - strconv.ParseBool で解釈できる場合は (parsed, true) を返す。
//   - 不正値の場合は (fallback, false) を返す。呼び出し側で警告ログなどの判断に利用する。
//
// env を直接読まず純粋な文字列を受け取るため、呼び出し側は os.Getenv 等で取得した値を渡す。
func ParseBoolString(raw string, fallback bool) (value bool, ok bool) {
	if raw == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, false
	}
	return parsed, true
}

// ParseLogFormat はログ出力を JSON にするか Text にするかを決定する。
//   - logFormatRaw が "json" / "text"（大小文字・前後空白は無視）の場合は
//     その指定に従い (useJSON, true) を返す。
//   - logFormatRaw が空文字の場合は appEnv にフォールバックし、
//     appEnv が "production" のとき JSON とする ((useJSON, true))。
//   - 上記以外の不正値の場合は appEnv ベースの既定値 + ok=false を返す。
//     呼び出し側で警告ログなどの判断に利用する。
//
// env を直接読まず純粋な文字列を受け取るため、呼び出し側は os.Getenv 等で取得した値を渡す。
func ParseLogFormat(logFormatRaw, appEnv string) (useJSON bool, ok bool) {
	defaultJSON := appEnv == "production"
	switch strings.ToLower(strings.TrimSpace(logFormatRaw)) {
	case "":
		return defaultJSON, true
	case "json":
		return true, true
	case "text":
		return false, true
	default:
		return defaultJSON, false
	}
}
