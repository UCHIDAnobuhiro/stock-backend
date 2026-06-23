// Package db はデータベース接続の初期化、リトライ、マイグレーションを提供します。
package db

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	// DefaultMaxOpenConns はコネクションプールの最大接続数のデフォルト値です。
	// database/sql のデフォルトは無制限のため、本番での接続枯渇を避けるべく上限を設けます。
	DefaultMaxOpenConns = 25
	// DefaultMaxIdleConns はアイドル接続数のデフォルト値です（database/sql のデフォルトは 2）。
	DefaultMaxIdleConns = 25
	// DefaultConnMaxLifetime は接続の最大生存時間のデフォルト値です。
	// Cloud SQL は古い接続を切るため、それより短い 5 分を既定とします。
	DefaultConnMaxLifetime = 5 * time.Minute
	// DefaultSSLMode は TCP 接続時の sslmode のデフォルト値です。
	DefaultSSLMode = "disable"
)

// Password はログ出力・文字列化・JSONシリアライズ時に値をマスクする機密文字列型です。
// fmt.Stringer / fmt.GoStringer / json.Marshaler / slog.LogValuer を実装しているため、
// 誤って構造体ごとログ出力しても平文パスワードは "***" に置換されます。
// DSN 構築など実値が必要な場合は string(p) で明示的に変換してください。
type Password string

// String は %s / %v などのフォーマット時にパスワードをマスクします。
func (Password) String() string { return "***" }

// GoString は %#v 書式でのマスク出力を提供します。
func (Password) GoString() string { return "***" }

// MarshalJSON は JSON シリアライズ時にパスワードをマスクします。
func (Password) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }

// LogValue は slog による構造化ログ出力時にパスワードをマスクします。
func (Password) LogValue() slog.Value { return slog.StringValue("***") }

// Config はデータベース接続設定を保持します。
type Config struct {
	User         string
	Password     Password
	Name         string
	Host         string
	Port         string
	InstanceName string // Cloud SQLインスタンス接続名（オプション）
	SSLMode      string // TCP 接続時の sslmode（空なら DefaultSSLMode）

	// コネクションプール設定。ゼロ値のときは Default* 定数にフォールバックします。
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Validate は Config の必須項目が設定されているかを検証します。
// InstanceName が設定されている場合は Cloud SQL 接続とみなし、Host/Port は不要です。
// それ以外の場合は TCP 接続として Host/Port を必須とします。
// Password は空でも許容します（ローカル開発で空パスワード運用を想定）。
func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.User) == "" {
		missing = append(missing, "DB_USER")
	}
	if strings.TrimSpace(c.Name) == "" {
		missing = append(missing, "DB_NAME")
	}
	if strings.TrimSpace(c.InstanceName) == "" {
		if strings.TrimSpace(c.Host) == "" {
			missing = append(missing, "DB_HOST")
		}
		if strings.TrimSpace(c.Port) == "" {
			missing = append(missing, "DB_PORT")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

// quotePGValue は libpq の key=value 形式で安全に値を埋め込めるようエスケープします。
// 値に空白・'='・シングルクオート・バックスラッシュが含まれる場合、または空値の場合は
// シングルクオートで囲み、内部の '\' と '\” をエスケープします。
// 参考: https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING-KEYWORD-VALUE
func quotePGValue(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\n\r='\\") {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `'`, `\'`)
		return "'" + escaped + "'"
	}
	return v
}

// BuildDSN は設定からPostgreSQL DSN文字列を構築します。
// InstanceNameが設定されている場合はCloud SQL Unixソケット接続を作成します。
// それ以外の場合はHostとPortを使用してTCP接続を作成します。
// 各値は libpq の仕様に従ってエスケープされるため、パスワード等に空白や特殊文字が
// 含まれていても安全に DSN を生成できます。
func BuildDSN(cfg Config) string {
	if cfg.InstanceName != "" {
		return fmt.Sprintf("host=%s user=%s password=%s dbname=%s",
			quotePGValue("/cloudsql/"+cfg.InstanceName),
			quotePGValue(cfg.User),
			quotePGValue(string(cfg.Password)),
			quotePGValue(cfg.Name))
	}
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = DefaultSSLMode
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		quotePGValue(cfg.Host),
		quotePGValue(cfg.Port),
		quotePGValue(cfg.User),
		quotePGValue(string(cfg.Password)),
		quotePGValue(cfg.Name),
		quotePGValue(sslMode))
}
