package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// TestCloudSQLDialOptions は DB_IP_TYPE ごとに Connector の接続先 IP 種別オプションを1つ返すことを検証します。
// DialOption は不透明な関数のため、件数だけを確認します。
func TestCloudSQLDialOptions(t *testing.T) {
	t.Parallel()

	for _, ipType := range []string{"", IPTypePublic, IPTypePrivate} {
		if got := len(cloudSQLDialOptions(ipType)); got != 1 {
			t.Errorf("ipType %q: expected 1 dial option, got %d", ipType, got)
		}
	}
}

// TestClosingConnector_ClosedByDBClose は *sql.DB の Close で Connector に結び付けた closer が
// 呼ばれ、dialer のバックグラウンド処理が停止されることを検証します。
func TestClosingConnector_ClosedByDBClose(t *testing.T) {
	t.Parallel()

	closer := &recordingCloser{}
	db := sql.OpenDB(&closingConnector{Connector: stubConnector{}, closer: closer})

	if err := db.Close(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if closer.calls != 1 {
		t.Errorf("expected closer to be called once, got %d", closer.calls)
	}
}

// TestClosingConnector_PropagatesCloseError は closer のエラーが DB.Close から返ることを検証します。
func TestClosingConnector_PropagatesCloseError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("close failed")
	db := sql.OpenDB(&closingConnector{Connector: stubConnector{}, closer: &recordingCloser{err: sentinel}})

	if err := db.Close(); !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// recordingCloser は Close の呼び出し回数を記録するテスト用 io.Closer です。
type recordingCloser struct {
	calls int
	err   error
}

func (c *recordingCloser) Close() error {
	c.calls++
	return c.err
}

// stubConnector は接続を確立しないテスト用 driver.Connector です。
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (stubConnector) Driver() driver.Driver { return nil }
