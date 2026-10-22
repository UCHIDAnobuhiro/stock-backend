package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"strings"
	"testing"

	"cloud.google.com/go/cloudsqlconn"
)

type testPrivateDialer struct {
	closeCount, dialCount int
	dialErr               error
}

func (d *testPrivateDialer) Dial(_ context.Context, _ string, _ ...cloudsqlconn.DialOption) (net.Conn, error) {
	d.dialCount++
	return nil, d.dialErr
}
func (d *testPrivateDialer) Close() error { d.closeCount++; return nil }

type noopConnector struct{}

func (noopConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("unexpected connect")
}
func (noopConnector) Driver() driver.Driver { return nil }

func TestPrivateModeRequiresInstanceAndRejectsUnknownMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		instance string
		want     string
	}{
		{"missing private instance", "private", "", "INSTANCE_CONNECTION_NAME"},
		{"missing socket instance", "socket", "", "INSTANCE_CONNECTION_NAME"},
		{"unknown mode", "public", "example:region:db", "DB_CONNECTION_MODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{User: "u", Name: "d", Host: "localhost", Port: "5432", ConnectionMode: tc.mode, InstanceName: tc.instance}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v; want %s", err, tc.want)
			}
		})
	}
	if err := (Config{User: "u", Name: "d", ConnectionMode: "private", InstanceName: "example:region:db"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedConnectorCloseViaDB(t *testing.T) {
	dialer := &testPrivateDialer{}
	db := sql.OpenDB(&ownedConnector{Connector: noopConnector{}, dialer: dialer})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if dialer.closeCount != 1 {
		t.Fatalf("dialer close count = %d; want 1", dialer.closeCount)
	}
}

func TestOpenPrivateSQLPingFailureClosesDialer(t *testing.T) {
	dialer := &testPrivateDialer{dialErr: errors.New("private route unavailable")}
	cfg := Config{User: "u", Name: "d", InstanceName: "example:region:db", ConnectionMode: "private"}
	db, err := openPrivateSQL(cfg, func(context.Context) (privateDialer, error) { return dialer, nil })
	if db != nil || err == nil || !strings.Contains(err.Error(), "private route unavailable") {
		t.Fatalf("openPrivateSQL() = %v, %v", db, err)
	}
	if dialer.dialCount < 1 || dialer.closeCount != 1 {
		t.Fatalf("dial/close counts = %d/%d; want at least one dial and one close", dialer.dialCount, dialer.closeCount)
	}
}

func TestOpenPrivateSQLFactoryFailure(t *testing.T) {
	sentinel := errors.New("credentials unavailable")
	cfg := Config{User: "u", Name: "d", InstanceName: "example:region:db", ConnectionMode: "private"}
	db, err := openPrivateSQL(cfg, func(context.Context) (privateDialer, error) { return nil, sentinel })
	if db != nil || !errors.Is(err, sentinel) {
		t.Fatalf("openPrivateSQL() = %v, %v", db, err)
	}
}
