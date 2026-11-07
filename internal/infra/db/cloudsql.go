package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// closingConnector は driver.Connector に dialer のクローズを結び付けます。
// database/sql は Connector が io.Closer を実装する場合、DB.Close 時にそれを呼び出します。
type closingConnector struct {
	driver.Connector
	closer io.Closer
}

// Close は DB.Close から呼ばれ、dialer のバックグラウンド処理を停止します。
func (c *closingConnector) Close() error {
	return c.closer.Close()
}

// newCloudSQLOpener は Cloud SQL Go Connector 経由で接続する SQLOpener を返します。
// Connector は Cloud SQL Admin API から取得した一時証明書でサーバー側プロキシ（3307）へ
// mTLS 接続し、roles/cloudsql.client による IAM 認可を経由させます。
// 試行ごとに dialer を作成し、*sql.DB の Close で dialer も閉じます。
func newCloudSQLOpener(instance, ipType string) SQLOpener {
	return func(dsn string) (*sql.DB, error) {
		// Cloud Run は CPU idle 時にバックグラウンドの証明書更新が止まるため、接続時に更新する。
		// 組み込みメトリクス（BETA）はランタイム SA に monitoring.metricWriter を要するため無効化する。
		dialer, err := cloudsqlconn.NewDialer(context.Background(),
			cloudsqlconn.WithLazyRefresh(),
			cloudsqlconn.WithBuiltInMetrics(false),
			cloudsqlconn.WithDefaultDialOptions(cloudSQLDialOptions(ipType)...),
		)
		if err != nil {
			return nil, fmt.Errorf("create Cloud SQL dialer: %w", err)
		}

		pgxCfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			_ = dialer.Close()
			return nil, fmt.Errorf("parse DSN: %w", err)
		}
		pgxCfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, instance)
		}

		db := sql.OpenDB(&closingConnector{Connector: stdlib.GetConnector(*pgxCfg), closer: dialer})
		return pingOrClose(db)
	}
}

// cloudSQLDialOptions は DB_IP_TYPE を Connector の接続先 IP 種別へ変換します。
// 値の妥当性は Config.Validate で検証済みのため、空と IPTypePublic は public IP として扱います。
func cloudSQLDialOptions(ipType string) []cloudsqlconn.DialOption {
	if ipType == IPTypePrivate {
		return []cloudsqlconn.DialOption{cloudsqlconn.WithPrivateIP()}
	}
	return []cloudsqlconn.DialOption{cloudsqlconn.WithPublicIP()}
}
