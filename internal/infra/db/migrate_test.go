//go:build !integration

package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openParkedDB は接続を開かない *sql.DB を返します。
// goose のコマンドディスパッチに渡す目的で使用し、実際の DB アクセスは伴いません。
func openParkedDB(t *testing.T) *sql.DB {
	t.Helper()
	// sql.Open は遅延接続のためここでは接続しない。
	db, err := sql.Open("pgx", "host=127.0.0.1 port=1 user=nouser dbname=nodb sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestRunGoose_UnknownCommand は未知のサブコマンドが渡された場合に
// goose のエラーが "goose <cmd>:" 形式でラップされることを検証します。
//
// goose は SetDialect / SetBaseFS でパッケージレベルのグローバル変数を書き換えるため、
// 本ファイル内のテストは t.Parallel() を呼びません。
func TestRunGoose_UnknownCommand(t *testing.T) {
	db := openParkedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := RunGoose(ctx, db, "no-such-command")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "goose no-such-command:") {
		t.Errorf("expected error to be wrapped with 'goose no-such-command:', got %q", err.Error())
	}
}

// TestRunGoose_UpToRequiresVersion は up-to が引数なしで呼ばれた場合に
// goose 側のエラーが返ることを検証します（DB アクセス前に弾かれる）。
func TestRunGoose_UpToRequiresVersion(t *testing.T) {
	db := openParkedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := RunGoose(ctx, db, "up-to")
	if err == nil {
		t.Fatal("expected error for up-to without version, got nil")
	}
	if !strings.Contains(err.Error(), "up-to") {
		t.Errorf("expected error to mention 'up-to', got %q", err.Error())
	}
}

// TestRunGoose_DownToRequiresNumericVersion は down-to に数値以外を渡した場合に
// goose 側のバリデーションエラーが返ることを検証します。
func TestRunGoose_DownToRequiresNumericVersion(t *testing.T) {
	db := openParkedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := RunGoose(ctx, db, "down-to", "not-a-number")
	if err == nil {
		t.Fatal("expected error for non-numeric version, got nil")
	}
	if !strings.Contains(err.Error(), "must be a number") {
		t.Errorf("expected numeric-version error, got %q", err.Error())
	}
}
