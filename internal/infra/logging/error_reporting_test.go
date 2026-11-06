package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func logErrorHelper(l *slog.Logger, args ...any) {
	l.Error("boom", args...)
}

// TestErrorReportingHandler は ERROR 以上への @type / reportLocation 付与と SkipErrorReport の除去を検証します。
func TestErrorReportingHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		logFunc  func(l *slog.Logger)
		wantType bool
		wantLoc  bool
		wantKeys map[string]any
	}{
		{
			name:     "error gets @type and reportLocation",
			logFunc:  func(l *slog.Logger) { logErrorHelper(l) },
			wantType: true,
			wantLoc:  true,
		},
		{
			name:    "warn is untouched",
			logFunc: func(l *slog.Logger) { l.Warn("careful") },
		},
		{
			name:    "info is untouched",
			logFunc: func(l *slog.Logger) { l.Info("hello") },
		},
		{
			name:    "error with skip marker has no @type",
			logFunc: func(l *slog.Logger) { l.LogAttrs(context.Background(), slog.LevelError, "boom", SkipErrorReport()) },
		},
		{
			name:    "info with skip marker drops marker",
			logFunc: func(l *slog.Logger) { l.LogAttrs(context.Background(), slog.LevelInfo, "hello", SkipErrorReport()) },
		},
		{
			name: "WithAttrs keeps attrs and still reports",
			logFunc: func(l *slog.Logger) {
				logErrorHelper(l.With("svc", "api"), "k", "v")
			},
			wantType: true,
			wantLoc:  true,
			wantKeys: map[string]any{"svc": "api", "k": "v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			tt.logFunc(slog.New(NewErrorReportingHandler(slog.NewJSONHandler(&buf, nil))))
			got := decodeLog(t, buf.Bytes())

			if _, ok := got[skipErrorReportKey]; ok {
				t.Errorf("skip marker leaked: %v", got)
			}
			if _, ok := got["@type"]; ok != tt.wantType {
				t.Errorf("@type present = %v, want %v (%v)", ok, tt.wantType, got)
			}
			if tt.wantType && got["@type"] != reportedErrorEventType {
				t.Errorf("@type = %v, want %v", got["@type"], reportedErrorEventType)
			}
			ctxVal, hasCtx := got["context"]
			if hasCtx != tt.wantLoc {
				t.Fatalf("context present = %v, want %v (%v)", hasCtx, tt.wantLoc, got)
			}
			if tt.wantLoc {
				loc := ctxVal.(map[string]any)["reportLocation"].(map[string]any)
				if p, _ := loc["filePath"].(string); !strings.HasSuffix(p, "error_reporting_test.go") {
					t.Errorf("filePath = %v", loc["filePath"])
				}
				if n, _ := loc["lineNumber"].(float64); n <= 0 {
					t.Errorf("lineNumber = %v", loc["lineNumber"])
				}
				if f, _ := loc["functionName"].(string); !strings.Contains(f, "logErrorHelper") {
					t.Errorf("functionName = %v", loc["functionName"])
				}
			}
			for k, v := range tt.wantKeys {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// TestErrorReportingHandler_ZeroPC は PC が 0 のとき @type のみ付き context は付かないことを検証します。
func TestErrorReportingHandler_ZeroPC(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := NewErrorReportingHandler(slog.NewJSONHandler(&buf, nil))
	if err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)); err != nil {
		t.Fatal(err)
	}

	got := decodeLog(t, buf.Bytes())
	if got["@type"] != reportedErrorEventType {
		t.Errorf("@type = %v", got["@type"])
	}
	if _, ok := got["context"]; ok {
		t.Errorf("context should be absent: %v", got)
	}
}
