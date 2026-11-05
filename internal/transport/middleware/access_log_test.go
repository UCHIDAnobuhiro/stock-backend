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

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
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

// TestAccessLog_LogsStructuredRequest はミドルウェアがリクエストを slog の構造化ログとして
// httpRequest フィールド付きで出力し、ステータスに応じた severity を設定することを検証します。
func TestAccessLog_LogsStructuredRequest(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	restore := swapDefaultLogger(&buf)
	defer restore()

	h := AccessLog()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

// TestAccessLog_IncludesTrace は TraceID → AccessLog を連結したとき、ContextHandler により
// アクセスログへ trace_id と Cloud Logging 用の相関フィールドが付与されることを検証します。
func TestAccessLog_IncludesTrace(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(logging.NewContextHandler(slog.NewJSONHandler(&buf, nil), "my-project")))

	h := TraceID()(AccessLog()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	traceID := w.Header().Get(TraceIDHeader)
	require.Len(t, traceID, 32)

	got := decodeLog(t, buf.Bytes())
	assert.Equal(t, traceID, got["trace_id"])
	assert.Equal(t, "projects/my-project/traces/"+traceID, got["logging.googleapis.com/trace"])
}

// TestAccessLog_ErrorStatusSeverity は 5xx 応答で severity が ERROR になることを検証します。
func TestAccessLog_ErrorStatusSeverity(t *testing.T) {
	// 並列化しない: slog.Default() というグローバルを差し替えるため。
	var buf bytes.Buffer
	restore := swapDefaultLogger(&buf)
	defer restore()

	h := AccessLog()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
