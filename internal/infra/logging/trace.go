package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// TraceContext はリクエスト単位のトレース情報を表します。
type TraceContext struct {
	// TraceID は 32 桁の小文字 16 進数。
	TraceID string
	// SpanID は 16 桁の小文字 16 進数。不明な場合は空。
	SpanID string
}

// traceContextKey は context.Context に TraceContext を格納するための非公開キー型です。
type traceContextKey struct{}

// WithTrace は tc を保持する子コンテキストを返します。
func WithTrace(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// TraceFrom は ctx に保持されたトレース情報を返します。
// 未設定または TraceID が空の場合は ok=false を返します。
func TraceFrom(ctx context.Context) (TraceContext, bool) {
	tc, ok := ctx.Value(traceContextKey{}).(TraceContext)
	if !ok || tc.TraceID == "" {
		return TraceContext{}, false
	}
	return tc, true
}

// NewTraceID は 32 桁の小文字 16 進数（16 バイト）のトレース ID を生成します。
// Go 1.24 以降 crypto/rand.Read はエラーを返さないため、戻り値は検査しません。
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
