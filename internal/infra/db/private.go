package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"sync"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// privateDialer is kept for the lifetime of one sql.DB, including all pooled connections.
type privateDialer interface {
	Dial(context.Context, string, ...cloudsqlconn.DialOption) (net.Conn, error)
	Close() error
}

type privateDialerFactory func(context.Context) (privateDialer, error)

func newCloudSQLDialer(ctx context.Context) (privateDialer, error) {
	return cloudsqlconn.NewDialer(ctx, cloudsqlconn.WithLazyRefresh())
}

// ownedConnector makes sql.DB.Close release the Cloud SQL dialer's refresh resources.
type ownedConnector struct {
	driver.Connector
	dialer   privateDialer
	once     sync.Once
	closeErr error
}

func (c *ownedConnector) Close() error {
	c.once.Do(func() { c.closeErr = c.dialer.Close() })
	return c.closeErr
}

func openPrivateSQL(cfg Config, factory privateDialerFactory) (*sql.DB, error) {
	// The connector authenticates and encrypts the network leg; PostgreSQL credentials
	// remain the existing DB_USER / DB_PASSWORD. No public IP fallback is attempted.
	dsn := BuildDSN(Config{User: cfg.User, Password: cfg.Password, Name: cfg.Name, Host: "localhost", Port: "5432"})
	pgcfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse private DB config: %w", err)
	}
	dialer, err := factory(context.Background())
	if err != nil {
		return nil, fmt.Errorf("create Cloud SQL dialer: %w", err)
	}
	pgcfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.Dial(ctx, cfg.InstanceName, cloudsqlconn.WithPrivateIP())
	}
	db := sql.OpenDB(&ownedConnector{Connector: stdlib.GetConnector(*pgcfg), dialer: dialer})
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping private Cloud SQL: %w", err)
	}
	return db, nil
}
