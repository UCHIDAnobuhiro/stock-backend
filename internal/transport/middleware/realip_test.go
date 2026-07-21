package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// TestRealIP は trustedHops の値ごとに X-Forwarded-For の解決結果・フォールバック挙動を検証します。
func TestRealIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		trustedHops int
		remoteAddr  string
		xff         []string // 複数指定すると複数の X-Forwarded-For ヘッダー行として送る
		wantIP      string
	}{
		{
			name:        "hops=0はXFFがあってもRemoteAddrを使う",
			trustedHops: 0,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, 5.6.7.8, 203.0.113.7"},
			wantIP:      "10.0.0.1",
		},
		{
			name:        "hops=1はXFF末尾を採用する（偽装エントリは無視）",
			trustedHops: 1,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, 5.6.7.8, 203.0.113.7"},
			wantIP:      "203.0.113.7",
		},
		{
			name:        "hops=2は右から2番目を採用する",
			trustedHops: 2,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, 5.6.7.8, 203.0.113.7"},
			wantIP:      "5.6.7.8",
		},
		{
			name:        "エントリ数が不足する場合はRemoteAddrにフォールバックする",
			trustedHops: 5,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, 5.6.7.8, 203.0.113.7"},
			wantIP:      "10.0.0.1",
		},
		{
			name:        "該当エントリが不正なIPならRemoteAddrにフォールバックする",
			trustedHops: 1,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, not-an-ip"},
			wantIP:      "10.0.0.1",
		},
		{
			name:        "複数のXFFヘッダー行は結合して評価する",
			trustedHops: 1,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4", "5.6.7.8, 203.0.113.7"},
			wantIP:      "203.0.113.7",
		},
		{
			name:        "IPv6のXFFエントリも解決できる",
			trustedHops: 1,
			remoteAddr:  "10.0.0.1:1234",
			xff:         []string{"1.2.3.4, 2001:db8::1"},
			wantIP:      "2001:db8::1",
		},
		{
			name:        "XFFヘッダーがない場合はRemoteAddrにフォールバックする",
			trustedHops: 1,
			remoteAddr:  "10.0.0.1:1234",
			xff:         nil,
			wantIP:      "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotIP string
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotIP = httpx.ClientIP(r)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			w := httptest.NewRecorder()

			RealIP(tt.trustedHops)(next).ServeHTTP(w, req)

			assert.Equal(t, tt.wantIP, gotIP)
		})
	}
}
