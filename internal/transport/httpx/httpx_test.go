package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClientIP は ClientIP が context 格納済みIPを優先し、
// 未格納の場合は RemoteAddr にフォールバックすることを検証します。
func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		remoteAddr   string
		contextIP    string // 空文字なら context に格納しない
		wantClientIP string
	}{
		{
			name:         "context にIPがあればそれを返す",
			remoteAddr:   "203.0.113.9:12345",
			contextIP:    "198.51.100.7",
			wantClientIP: "198.51.100.7",
		},
		{
			name:         "context が空なら RemoteAddr のホスト部を返す",
			remoteAddr:   "203.0.113.9:12345",
			contextIP:    "",
			wantClientIP: "203.0.113.9",
		},
		{
			name:         "RemoteAddr にポートがなければそのまま返す",
			remoteAddr:   "203.0.113.9",
			contextIP:    "",
			wantClientIP: "203.0.113.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.contextIP != "" {
				req = req.WithContext(WithClientIP(req.Context(), tt.contextIP))
			}

			assert.Equal(t, tt.wantClientIP, ClientIP(req))
		})
	}
}
