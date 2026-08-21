package logging

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"strings"
	"testing"
)

// TestNewCloudLoggingHandler_RemapsTopLevelKeys は level / msg が Cloud Logging の
// severity / message にリマップされ、WARN が WARNING に変換されることを検証します。
func TestNewCloudLoggingHandler_RemapsTopLevelKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		logFunc      func(l *slog.Logger)
		wantSeverity string
		wantMessage  string
	}{
		{
			name:         "info",
			logFunc:      func(l *slog.Logger) { l.Info("hello") },
			wantSeverity: "INFO",
			wantMessage:  "hello",
		},
		{
			name:         "warn maps to WARNING",
			logFunc:      func(l *slog.Logger) { l.Warn("careful") },
			wantSeverity: "WARNING",
			wantMessage:  "careful",
		},
		{
			name:         "error",
			logFunc:      func(l *slog.Logger) { l.Error("boom") },
			wantSeverity: "ERROR",
			wantMessage:  "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(NewCloudLoggingHandler(&buf, slog.LevelInfo))
			tt.logFunc(logger)

			got := decodeLog(t, buf.Bytes())

			if got["severity"] != tt.wantSeverity {
				t.Errorf("severity = %v, want %v", got["severity"], tt.wantSeverity)
			}
			if got["message"] != tt.wantMessage {
				t.Errorf("message = %v, want %v", got["message"], tt.wantMessage)
			}
			// 既定キーが残っていないこと。
			if _, ok := got["level"]; ok {
				t.Errorf("unexpected 'level' key present: %v", got)
			}
			if _, ok := got["msg"]; ok {
				t.Errorf("unexpected 'msg' key present: %v", got)
			}
		})
	}
}

// TestNewCloudLoggingHandler_LeavesNestedKeys はネストしたグループ内のフィールドが
// リマップ対象外であることを検証します（httpRequest の msg/level 等を壊さないため）。
func TestNewCloudLoggingHandler_LeavesNestedKeys(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewCloudLoggingHandler(&buf, slog.LevelInfo))
	logger.Info("request", slog.Any("httpRequest", map[string]any{
		"requestMethod": "GET",
		"status":        200,
	}))

	got := decodeLog(t, buf.Bytes())

	req, ok := got["httpRequest"].(map[string]any)
	if !ok {
		t.Fatalf("httpRequest missing or wrong type: %v", got["httpRequest"])
	}
	if req["requestMethod"] != "GET" {
		t.Errorf("httpRequest.requestMethod = %v, want GET", req["requestMethod"])
	}
}

// TestNewCloudLoggingHandler_RespectsLevel は指定レベル未満のログが出力されないことを検証します。
func TestNewCloudLoggingHandler_RespectsLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewCloudLoggingHandler(&buf, slog.LevelInfo))
	logger.Debug("should be dropped")

	if buf.Len() != 0 {
		t.Errorf("expected no output for debug below info level, got: %s", buf.String())
	}
}

// TestNewHandler_JSON は useJSON=true で Cloud Logging 対応の JSON を出力することを検証します。
func TestNewHandler_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo, true))
	logger.Info("hello")

	got := decodeLog(t, buf.Bytes())
	if got["severity"] != "INFO" {
		t.Errorf("severity = %v, want INFO", got["severity"])
	}
	if got["message"] != "hello" {
		t.Errorf("message = %v, want hello", got["message"])
	}
}

// TestNewHandler_Text は useJSON=false でプレーンな Text 形式（JSON ではない・既定キー）を
// 出力することを検証します。
func TestNewHandler_Text(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, slog.LevelInfo, false))
	logger.Info("hello")

	out := buf.String()
	if jsontext.Value(bytes.TrimSpace(buf.Bytes())).IsValid() {
		t.Errorf("expected non-JSON text output, got: %s", out)
	}
	// Text 形式はリマップせず既定キー（level / msg）のまま。
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected 'level=INFO' in text output, got: %s", out)
	}
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("expected 'msg=hello' in text output, got: %s", out)
	}
}

func decodeLog(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("failed to parse log JSON: %v (raw=%s)", err, b)
	}
	return got
}
