package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
)

// AdvisoryLock はPostgreSQLのセッションレベルadvisory lockを保持します。
// lock取得に使った接続を固定し、同じセッションでunlockすることを保証します。
type AdvisoryLock struct {
	mu        sync.Mutex
	conn      *sql.Conn
	namespace int32
	key       int32
}

// TryAdvisoryLock は待機せずにadvisory lockの取得を試みます。
// acquiredがfalseの場合、同じキーのlockが既に保持されており、lockはnilです。
func TryAdvisoryLock(
	ctx context.Context,
	db *sql.DB,
	namespace int32,
	key int32,
) (lock *AdvisoryLock, acquired bool, err error) {
	if db == nil {
		return nil, false, errors.New("try advisory lock: db is nil")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get advisory lock connection: %w", err)
	}

	if err := conn.QueryRowContext(
		ctx,
		`SELECT pg_try_advisory_lock($1, $2)`,
		namespace,
		key,
	).Scan(&acquired); err != nil {
		discardErr := discardAdvisoryLockConnection(conn)
		return nil, false, errors.Join(
			fmt.Errorf("try advisory lock: %w", err),
			discardErr,
		)
	}
	if !acquired {
		if err := conn.Close(); err != nil {
			return nil, false, fmt.Errorf("close unused advisory lock connection: %w", err)
		}
		return nil, false, nil
	}

	return &AdvisoryLock{
		conn:      conn,
		namespace: namespace,
		key:       key,
	}, true, nil
}

// Unlock はlockを取得したセッションでadvisory lockを解放します。
// 複数回呼ばれても2回目以降は何もしません。
func (l *AdvisoryLock) Unlock(ctx context.Context) error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}

	conn := l.conn
	l.conn = nil

	var unlocked bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT pg_advisory_unlock($1, $2)`,
		l.namespace,
		l.key,
	).Scan(&unlocked); err != nil {
		discardErr := discardAdvisoryLockConnection(conn)
		return errors.Join(
			fmt.Errorf("unlock advisory lock: %w", err),
			discardErr,
		)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close advisory lock connection: %w", err)
	}
	if !unlocked {
		return errors.New("unlock advisory lock: lock was not held")
	}
	return nil
}

// discardAdvisoryLockConnection は結果を受け取れなかったlock操作がサーバー側で成功していても、
// セッションをpoolへ戻さず物理接続ごと破棄してlockを確実に解放します。
func discardAdvisoryLockConnection(conn *sql.Conn) error {
	return conn.Raw(func(any) error { return driver.ErrBadConn })
}
