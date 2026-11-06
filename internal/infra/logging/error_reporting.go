package logging

import (
	"context"
	"log/slog"
	"runtime"
)

const (
	reportedErrorEventType = "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"
	skipErrorReportKey     = "logging.internal/skip_error_report"
)

// errorReportingHandler は ERROR 以上のログを Cloud Error Reporting の対象にする slog.Handler です。
type errorReportingHandler struct {
	inner slog.Handler
}

// NewErrorReportingHandler は ERROR 以上のログに ReportedErrorEvent の @type と
// reportLocation（ログ出力元）を付与し、Cloud Error Reporting に集約させる slog.Handler を返します。
func NewErrorReportingHandler(inner slog.Handler) slog.Handler {
	return &errorReportingHandler{inner: inner}
}

// SkipErrorReport は Error Reporting への集約を除外する目印の属性を返します。
// NewErrorReportingHandler が取り除くため、出力には残りません。
func SkipErrorReport() slog.Attr {
	return slog.Bool(skipErrorReportKey, true)
}

// Enabled は内側のハンドラーに委譲します。
func (h *errorReportingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle は目印付きレコードから目印を除去し、それ以外の ERROR 以上に @type と reportLocation を付与します。
func (h *errorReportingHandler) Handle(ctx context.Context, rec slog.Record) error {
	if stripped, ok := withoutSkipMarker(rec); ok {
		return h.inner.Handle(ctx, stripped)
	}
	if rec.Level >= slog.LevelError {
		rec = rec.Clone()
		rec.AddAttrs(slog.String("@type", reportedErrorEventType))
		if rec.PC != 0 {
			frame, _ := runtime.CallersFrames([]uintptr{rec.PC}).Next()
			rec.AddAttrs(slog.Group("context", slog.Group("reportLocation",
				slog.String("filePath", frame.File),
				slog.Int("lineNumber", frame.Line),
				slog.String("functionName", frame.Function),
			)))
		}
	}
	return h.inner.Handle(ctx, rec)
}

// WithAttrs は属性を追加した内側ハンドラーを、再び errorReportingHandler で包んで返します。
func (h *errorReportingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &errorReportingHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup はグループを開いた内側ハンドラーを、再び errorReportingHandler で包んで返します。
func (h *errorReportingHandler) WithGroup(name string) slog.Handler {
	return &errorReportingHandler{inner: h.inner.WithGroup(name)}
}

// withoutSkipMarker は目印を除いたレコードを返します。目印が無ければ ok は false です。
func withoutSkipMarker(rec slog.Record) (slog.Record, bool) {
	found := false
	rec.Attrs(func(a slog.Attr) bool {
		found = a.Key == skipErrorReportKey
		return !found
	})
	if !found {
		return rec, false
	}
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key != skipErrorReportKey {
			out.AddAttrs(a)
		}
		return true
	})
	return out, true
}
