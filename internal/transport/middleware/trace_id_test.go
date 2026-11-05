package middleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
)

func TestTraceID(t *testing.T) {
	t.Parallel()

	const (
		traceLower = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanHex    = "00f067aa0ba902b7"
		zeroTrace  = "00000000000000000000000000000000"
	)
	generated := regexp.MustCompile(`^[0-9a-f]{32}$`)

	tests := []struct {
		name      string
		headers   map[string]string
		wantTrace string // 空なら生成値（32 桁小文字 16 進）を期待する
		wantSpan  string
	}{
		{
			name:      "valid traceparent",
			headers:   map[string]string{"traceparent": "00-" + traceLower + "-" + spanHex + "-01"},
			wantTrace: traceLower,
			wantSpan:  spanHex,
		},
		{
			name:    "traceparent bad length",
			headers: map[string]string{"traceparent": "00-abc-" + spanHex + "-01"},
		},
		{
			name:    "traceparent non-hex",
			headers: map[string]string{"traceparent": "00-" + "zbf92f3577b34da6a3ce929d0e0e4736" + "-" + spanHex + "-01"},
		},
		{
			name:    "traceparent all-zero trace",
			headers: map[string]string{"traceparent": "00-" + zeroTrace + "-" + spanHex + "-01"},
		},
		{
			name:    "traceparent all-zero span",
			headers: map[string]string{"traceparent": "00-" + traceLower + "-0000000000000000-01"},
		},
		{
			name: "invalid traceparent falls back to X-Cloud-Trace-Context",
			headers: map[string]string{
				"traceparent":           "garbage",
				"X-Cloud-Trace-Context": traceLower + "/255;o=1",
			},
			wantTrace: traceLower,
			wantSpan:  "00000000000000ff",
		},
		{
			name:      "XCTC with decimal span",
			headers:   map[string]string{"X-Cloud-Trace-Context": traceLower + "/11803058438906374;o=1"},
			wantTrace: traceLower,
			wantSpan:  "0029eed1b183a206",
		},
		{
			name:      "XCTC uppercase trace is normalized",
			headers:   map[string]string{"X-Cloud-Trace-Context": "4BF92F3577B34DA6A3CE929D0E0E4736/1"},
			wantTrace: traceLower,
			wantSpan:  "0000000000000001",
		},
		{
			name:      "XCTC without span",
			headers:   map[string]string{"X-Cloud-Trace-Context": traceLower},
			wantTrace: traceLower,
		},
		{
			name:      "XCTC with invalid span keeps trace",
			headers:   map[string]string{"X-Cloud-Trace-Context": traceLower + "/abc;o=1"},
			wantTrace: traceLower,
		},
		{
			name:    "XCTC non-hex",
			headers: map[string]string{"X-Cloud-Trace-Context": "zzzz/1"},
		},
		{
			name:    "XCTC all zeros",
			headers: map[string]string{"X-Cloud-Trace-Context": zeroTrace + "/1"},
		},
		{
			name:    "XCTC injection attempt",
			headers: map[string]string{"X-Cloud-Trace-Context": "abc\nfoo"},
		},
		{
			name: "traceparent wins over XCTC",
			headers: map[string]string{
				"traceparent":           "00-" + traceLower + "-" + spanHex + "-01",
				"X-Cloud-Trace-Context": "ffffffffffffffffffffffffffffffff/1",
			},
			wantTrace: traceLower,
			wantSpan:  spanHex,
		},
		{
			name: "no headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got logging.TraceContext
			var ok bool
			h := TraceID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, ok = logging.TraceFrom(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header[http.CanonicalHeaderKey(k)] = []string{v}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.True(t, ok)
			if tt.wantTrace != "" {
				assert.Equal(t, tt.wantTrace, got.TraceID)
			} else {
				assert.Regexp(t, generated, got.TraceID)
			}
			assert.Equal(t, tt.wantSpan, got.SpanID)
			assert.Equal(t, got.TraceID, w.Header().Get(TraceIDHeader))
		})
	}
}
