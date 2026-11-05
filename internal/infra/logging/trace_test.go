package logging

import (
	"context"
	"regexp"
	"testing"
)

var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestNewTraceID(t *testing.T) {
	t.Parallel()

	a, b := NewTraceID(), NewTraceID()
	for _, id := range []string{a, b} {
		if !traceIDPattern.MatchString(id) {
			t.Errorf("NewTraceID() = %q, want 32 lowercase hex", id)
		}
	}
	if a == b {
		t.Errorf("NewTraceID() returned the same value twice: %q", a)
	}
}

func TestWithTrace_TraceFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		want   TraceContext
		wantOK bool
	}{
		{
			name:   "round trip",
			ctx:    WithTrace(context.Background(), TraceContext{TraceID: "abc", SpanID: "def"}),
			want:   TraceContext{TraceID: "abc", SpanID: "def"},
			wantOK: true,
		},
		{
			name:   "empty context",
			ctx:    context.Background(),
			wantOK: false,
		},
		{
			name:   "empty trace id is treated as absent",
			ctx:    WithTrace(context.Background(), TraceContext{SpanID: "def"}),
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := TraceFrom(tt.ctx)
			if ok != tt.wantOK {
				t.Fatalf("TraceFrom() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("TraceFrom() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
