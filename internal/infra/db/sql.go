package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx" の登録
)

// SQLOpener は database/sql のコネクションを開く関数型です。
// テストでモック化するために関数型として定義します。
type SQLOpener func(dsn string) (*sql.DB, error)

// OpenSQL は渡された設定を検証して *sql.DB を返します。
// リトライロジックを含み、設定不正や接続失敗は呼び出し元へ返します。
// 設定の読み込み（環境変数）は internal/app/config に集約されています。
func OpenSQL(cfg Config) (*sql.DB, error) {
	return openSQLWithRetry(cfg, 60*time.Second, DefaultSQLOpener)
}

// openSQLWithRetry は OpenSQL の検証と接続処理を実行します。
// timeout と opener を受け取り、異常系を短時間でテストできるようにします。
func openSQLWithRetry(cfg Config, timeout time.Duration, opener SQLOpener) (*sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid DB config: %w", err)
	}
	if cfg.InstanceName != "" && (cfg.Host != "" || cfg.Port != "") {
		slog.Warn("DB_HOST and DB_PORT are ignored when INSTANCE_CONNECTION_NAME is set",
			"host", cfg.Host, "port", cfg.Port, "instance", cfg.InstanceName)
	}
	dsn := BuildDSN(cfg)

	return ConnectSQLWithRetry(dsn, timeout, opener)
}

// ConnectSQLWithRetry はリトライ付きで *sql.DB を取得します。
// timeout 期間中、3秒間隔で再試行します。
func ConnectSQLWithRetry(dsn string, timeout time.Duration, opener SQLOpener) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	for {
		db, err := opener(dsn)
		if err == nil {
			return db, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("DB connect failed after %v: %w", timeout, err)
		}
		slog.Warn("DB connect failed, retrying", "error", err)
		time.Sleep(3 * time.Second)
	}
}

// DefaultSQLOpener は pgx/v5/stdlib driver で PostgreSQL に接続する SQLOpener です。
// 接続プールのデフォルト値はそのままで、必要に応じて呼び出し側で SetMaxOpenConns 等を調整します。
func DefaultSQLOpener(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// sql.Open はコネクション確立を遅延するため、Ping で疎通確認する。
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
