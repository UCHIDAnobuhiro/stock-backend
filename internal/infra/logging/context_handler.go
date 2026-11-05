package logging

import (
	"context"
	"log/slog"
)

// contextHandler は context.Context 上のトレース情報をログレコードに付与する slog.Handler です。
type contextHandler struct {
	inner     slog.Handler
	projectID string
}

// NewContextHandler は ctx に WithTrace で保持されたトレース情報を、
// ログ出力時に自動で付与する slog.Handler を返します。
//
// trace_id 属性を常に付与します。projectID が空でない場合は Cloud Logging の相関用に
// logging.googleapis.com/trace（projects/<projectID>/traces/<id>）と、SpanID があれば
// logging.googleapis.com/spanId も付与します。
func NewContextHandler(inner slog.Handler, projectID string) slog.Handler {
	return &contextHandler{inner: inner, projectID: projectID}
}

// Enabled は内側のハンドラーに委譲します。
func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle は ctx にトレース情報があれば属性を追加して内側のハンドラーに委譲します。
func (h *contextHandler) Handle(ctx context.Context, rec slog.Record) error {
	if tc, ok := TraceFrom(ctx); ok {
		rec = rec.Clone()
		rec.AddAttrs(slog.String("trace_id", tc.TraceID))
		if h.projectID != "" {
			rec.AddAttrs(slog.String("logging.googleapis.com/trace", "projects/"+h.projectID+"/traces/"+tc.TraceID))
			if tc.SpanID != "" {
				rec.AddAttrs(slog.String("logging.googleapis.com/spanId", tc.SpanID))
			}
		}
	}
	return h.inner.Handle(ctx, rec)
}

// WithAttrs は属性を追加した内側ハンドラーを、再び contextHandler で包んで返します。
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs), projectID: h.projectID}
}

// WithGroup はグループを開いた内側ハンドラーを、再び contextHandler で包んで返します。
//
// 注意: WithGroup 以降に出力されるログでは、Handle で追加するトレース属性も
// そのグループの内側に入ります（このリポジトリでは現状 WithGroup を使用していません）。
func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name), projectID: h.projectID}
}
