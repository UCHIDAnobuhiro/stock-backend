package middleware

import (
	"bytes"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeverityForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   slog.Level
	}{
		{http.StatusOK, slog.LevelInfo},
		{http.StatusMovedPermanently, slog.LevelInfo},
		{http.StatusBadRequest, slog.LevelWarn},
		{http.StatusNotFound, slog.LevelWarn},
		{http.StatusInternalServerError, slog.LevelError},
		{http.StatusBadGateway, slog.LevelError},
	}

	for _, tt := range tests {
		if got := severityForStatus(tt.status); got != tt.want {
			t.Errorf("severityForStatus(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestTraceContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		projectID string
		header    string
		wantTrace string
		wantSpan  string
		wantOK    bool
	}{
		{
			name:      "full header with span and options",
			projectID: "my-proj",
			header:    "abc123/456;o=1",
			wantTrace: "projects/my-proj/traces/abc123",
			wantSpan:  "456",
			wantOK:    true,
		},
		{
			name:      "trace id only",
			projectID: "my-proj",
			header:    "abc123",
			wantTrace: "projects/my-proj/traces/abc123",
			wantSpan:  "",
			wantOK:    true,
		},
		{
			name:      "empty project disables trace",
			projectID: "",
			header:    "abc123/456;o=1",
			wantOK:    false,
		},
		{
			name:      "empty header",
			projectID: "my-proj",
			header:    "",
			wantOK:    false,
		},
		{
			name:      "empty trace id",
			projectID: "my-proj",
			header:    "/456;o=1",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trace, span, ok := traceContext(tt.projectID, tt.header)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantTrace, trace)
				assert.Equal(t, tt.wantSpan, span)
			}
		})
	}
}

// TestAccessLog_LogsStructuredRequest はミドルウェアがリクエストを slog の構造化ログとして
// httpRequest フィールド付きで出力し、ステータスに応じた severity を設定することを検証します。
func TestAccessLog_LogsStructuredRequest(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	restore := swapDefaultLogger(&buf)
	defer restore()

	h := AccessLog("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/symbols?interval=1day", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	got := decodeLog(t, buf.Bytes())
	assert.Equal(t, "INFO", got["severity"])
	assert.Equal(t, "request", got["message"])

	httpReq, ok := got["httpRequest"].(map[string]any)
	require.True(t, ok, "httpRequest field missing: %v", got)
	assert.Equal(t, http.MethodGet, httpReq["requestMethod"])
	assert.Equal(t, "/v1/symbols?interval=1day", httpReq["requestUrl"])
	assert.EqualValues(t, http.StatusOK, httpReq["status"])

	// projectID が空のためトレースフィールドは出力されない。
	_, hasTrace := got["logging.googleapis.com/trace"]
	assert.False(t, hasTrace)
}

// TestAccessLog_IncludesTrace は projectID と X-Cloud-Trace-Context が揃っている場合に
// トレース相関フィールドが出力されることを検証します。
func TestAccessLog_IncludesTrace(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	restore := swapDefaultLogger(&buf)
	defer restore()

	h := AccessLog("my-proj")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Cloud-Trace-Context", "trace-abc/span-1;o=1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	got := decodeLog(t, buf.Bytes())
	assert.Equal(t, "projects/my-proj/traces/trace-abc", got["logging.googleapis.com/trace"])
	assert.Equal(t, "span-1", got["logging.googleapis.com/spanId"])
}

// TestAccessLog_ErrorStatusSeverity は 5xx 応答で severity が ERROR になることを検証します。
func TestAccessLog_ErrorStatusSeverity(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	restore := swapDefaultLogger(&buf)
	defer restore()

	h := AccessLog("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/fail", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	got := decodeLog(t, buf.Bytes())
	assert.Equal(t, "ERROR", got["severity"])
}

// swapDefaultLogger は slog のデフォルトロガーをバッファ出力に差し替え、復元関数を返します。
// テスト対象のミドルウェアが slog.LogAttrs を使うため、出力をキャプチャするのに使います。
func swapDefaultLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	// 本番と同じ severity/message リマップを再現するため簡易ハンドラを使う。
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})))
	return func() { slog.SetDefault(prev) }
}

func decodeLog(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("failed to parse log JSON: %v (raw=%s)", err, b)
	}
	return got
}
