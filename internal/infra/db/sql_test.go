package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestConnectSQLWithRetry_SuccessOnFirstTry は初回接続成功時にリトライせず DB を返すことを検証します。
func TestConnectSQLWithRetry_SuccessOnFirstTry(t *testing.T) {
	t.Parallel()

	mockDB := &sql.DB{}
	calls := 0
	opener := func(dsn string) (*sql.DB, error) {
		calls++
		return mockDB, nil
	}

	db, err := ConnectSQLWithRetry("test-dsn", 5*time.Second, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db != mockDB {
		t.Error("expected mock DB to be returned")
	}
	if calls != 1 {
		t.Errorf("expected 1 attempt, got %d", calls)
	}
}

// TestConnectSQLWithRetry_RetriesOnFailure は接続失敗時にリトライして最終的に成功することを検証します。
func TestConnectSQLWithRetry_RetriesOnFailure(t *testing.T) {
	// retry interval が 3 秒固定のため Parallel にしない
	mockDB := &sql.DB{}
	attemptCount := 0
	opener := func(dsn string) (*sql.DB, error) {
		attemptCount++
		if attemptCount < 3 {
			return nil, errors.New("connection refused")
		}
		return mockDB, nil
	}

	db, err := ConnectSQLWithRetry("test-dsn", 10*time.Second, opener)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db != mockDB {
		t.Error("expected mock DB to be returned")
	}
	if attemptCount != 3 {
		t.Errorf("expected 3 attempts, got %d", attemptCount)
	}
}

// TestConnectSQLWithRetry_TimeoutAfterRetries はタイムアウト後にエラーが返されることを検証します。
func TestConnectSQLWithRetry_TimeoutAfterRetries(t *testing.T) {
	t.Parallel()

	attemptCount := 0
	opener := func(dsn string) (*sql.DB, error) {
		attemptCount++
		return nil, errors.New("connection refused")
	}

	_, err := ConnectSQLWithRetry("test-dsn", 100*time.Millisecond, opener)
	if err == nil {
		t.Fatal("expected error after timeout, got nil")
	}
	if attemptCount == 0 {
		t.Error("expected at least one connection attempt")
	}
}

// TestConnectSQLWithRetry_ErrorWrapped はタイムアウト時のエラーが最後の opener エラーをラップしていることを検証します。
func TestConnectSQLWithRetry_ErrorWrapped(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("driver-specific failure")
	opener := func(dsn string) (*sql.DB, error) {
		return nil, sentinel
	}

	_, err := ConnectSQLWithRetry("test-dsn", 50*time.Millisecond, opener)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
}

// TestConfigurePool_AppliesDefaults は cfg の値がゼロのとき Default* がプールに適用されることを検証します。
// MaxIdleConns / ConnMaxLifetime は database/sql に getter がないため、検証可能な MaxOpenConnections を確認します。
func TestConfigurePool_AppliesDefaults(t *testing.T) {
	t.Parallel()

	db := &sql.DB{}
	configurePool(db, Config{})

	if got := db.Stats().MaxOpenConnections; got != DefaultMaxOpenConns {
		t.Errorf("expected MaxOpenConnections %d, got %d", DefaultMaxOpenConns, got)
	}
}

// TestConfigurePool_AppliesExplicit は cfg に明示された値がプールに適用されることを検証します。
func TestConfigurePool_AppliesExplicit(t *testing.T) {
	t.Parallel()

	db := &sql.DB{}
	configurePool(db, Config{MaxOpenConns: 7, MaxIdleConns: 3, ConnMaxLifetime: time.Minute})

	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("expected MaxOpenConnections 7, got %d", got)
	}
}

func TestOpenSQL_InvalidConfig(t *testing.T) {
	t.Parallel()

	// User 未設定の不正な Config（環境変数の読み込みは config パッケージに集約済み）
	_, err := OpenSQL(Config{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid DB config") {
		t.Errorf("expected invalid config error, got %v", err)
	}
}

func TestOpenSQLWithRetry_ConnectionFailure(t *testing.T) {
	t.Parallel()

	cfg := Config{
		User:     "appuser",
		Password: "apppass",
		Name:     "app",
		Host:     "localhost",
		Port:     "5432",
	}

	sentinel := errors.New("connection refused")
	calls := 0
	opener := func(dsn string) (*sql.DB, error) {
		calls++
		return nil, sentinel
	}

	_, err := openSQLWithRetry(cfg, 0, opener)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 attempt, got %d", calls)
	}
}
