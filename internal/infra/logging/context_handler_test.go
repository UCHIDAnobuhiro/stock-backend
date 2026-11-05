package logging

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"log/slog"
	"testing"
)

const (
	keyTrace  = "logging.googleapis.com/trace"
	keySpanID = "logging.googleapis.com/spanId"
)

func TestContextHandler_Handle(t *testing.T) {
	t.Parallel()

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	tests := []struct {
		name      string
		projectID string
		ctx       context.Context
		want      map[string]string
		wantAbsen []string
	}{
		{
			name:      "trace with project id",
			projectID: "my-proj",
			ctx:       WithTrace(context.Background(), TraceContext{TraceID: traceID, SpanID: "00f067aa0ba902b7"}),
			want: map[string]string{
				"trace_id": traceID,
				keyTrace:   "projects/my-proj/traces/" + traceID,
				keySpanID:  "00f067aa0ba902b7",
			},
		},
		{
			name: "trace without project id",
			ctx:  WithTrace(context.Background(), TraceContext{TraceID: traceID, SpanID: "00f067aa0ba902b7"}),
			want: map[string]string{"trace_id": traceID},
			wantAbsen: []string{
				keyTrace, keySpanID,
			},
		},
		{
			name:      "empty span id",
			projectID: "my-proj",
			ctx:       WithTrace(context.Background(), TraceContext{TraceID: traceID}),
			want: map[string]string{
				"trace_id": traceID,
				keyTrace:   "projects/my-proj/traces/" + traceID,
			},
			wantAbsen: []string{keySpanID},
		},
		{
			name:      "no trace in context",
			projectID: "my-proj",
			ctx:       context.Background(),
			wantAbsen: []string{"trace_id", keyTrace, keySpanID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil), tt.projectID))
			logger.InfoContext(tt.ctx, "hello")

			got := decodeJSON(t, buf.Bytes())
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
			for _, k := range tt.wantAbsen {
				if _, ok := got[k]; ok {
					t.Errorf("%s should be absent: %v", k, got)
				}
			}
		})
	}
}

func TestContextHandler_WithAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil), "")).With("k", "v")
	ctx := WithTrace(context.Background(), TraceContext{TraceID: "abc"})
	logger.InfoContext(ctx, "hello")

	got := decodeJSON(t, buf.Bytes())
	if got["k"] != "v" || got["trace_id"] != "abc" {
		t.Errorf("unexpected log: %v", got)
	}
}

func TestContextHandler_Enabled(t *testing.T) {
	t.Parallel()

	inner := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewContextHandler(inner, "")

	tests := []struct {
		level slog.Level
		want  bool
	}{
		{slog.LevelInfo, false},
		{slog.LevelWarn, true},
		{slog.LevelError, true},
	}
	for _, tt := range tests {
		if got := h.Enabled(context.Background(), tt.level); got != tt.want {
			t.Errorf("Enabled(%v) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("failed to parse log JSON: %v (raw=%s)", err, b)
	}
	return got
}
