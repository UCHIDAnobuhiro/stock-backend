package jwt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
)

// TestRevokeRequestToken_ValidToken は有効なトークンを持つリクエストに対し、
// jtiがブラックリストへ登録される（Revokeが呼ばれる）ことを検証します。
func TestRevokeRequestToken_ValidToken(t *testing.T) {
	t.Parallel()

	const testSecret = "test-secret-for-revoke-request"
	const jti = "logout-jti-1"
	token := createTokenWithJTI(testSecret, 1, time.Hour, jti)

	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })

	// ttl はリクエスト処理にかかる時間の分だけ厳密な一致が期待できないため、
	// キーが一致していることのみを CustomMatch で検証する。
	match := mock.CustomMatch(func(_, actual []interface{}) error {
		if len(actual) < 2 {
			t.Fatalf("unexpected SET args: %+v", actual)
		}
		if actual[1] != blacklistKey(jti) {
			t.Errorf("expected key %q, got %v", blacklistKey(jti), actual[1])
		}
		return nil
	})
	match.ExpectSet("ignored", "1", time.Hour).SetVal("OK")

	req := httptest.NewRequest(http.MethodDelete, "/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	bl := NewBlacklist(rdb)
	if err := RevokeRequestToken(context.Background(), req, testSecret, bl); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// TestRevokeRequestToken_NoopCases はトークンなし・不正トークン・期限切れトークンの場合に
// Redisへ書き込まず、エラーも返さないことを検証します（ログアウトはCookie削除が主目的のため）。
func TestRevokeRequestToken_NoopCases(t *testing.T) {
	t.Parallel()

	const testSecret = "test-secret-for-revoke-noop"

	tests := []struct {
		name   string
		mutate func(r *http.Request)
	}{
		{"no token", func(r *http.Request) {}},
		{"malformed token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer not-a-valid-token")
		}},
		{"wrong secret", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+createTokenWithJTI("wrong-secret", 1, time.Hour, "jti"))
		}},
		{"expired token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+createTokenWithJTI(testSecret, 1, -time.Hour, "jti"))
		}},
		{"missing jti", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+createTokenWithSecret(testSecret, 1, time.Hour))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdb, mock := redismock.NewClientMock()
			t.Cleanup(func() { _ = rdb.Close() })

			req := httptest.NewRequest(http.MethodDelete, "/logout", nil)
			tt.mutate(req)

			bl := NewBlacklist(rdb)
			if err := RevokeRequestToken(context.Background(), req, testSecret, bl); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("expected no Redis calls, but: %v", err)
			}
		})
	}
}
