package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
)

// TraceIDHeader はトレース ID を返すレスポンスヘッダー名です。
const TraceIDHeader = "X-Trace-Id"

// TraceID はリクエストごとにトレース ID を解決して r.Context() に保持し、
// レスポンスヘッダー X-Trace-Id でも返すミドルウェアを返します。
//
// 解決順は、(1) W3C traceparent、(2) X-Cloud-Trace-Context、(3) 新規生成です。
// 不正な値はログインジェクション対策のため破棄します。
// ログへの付与は logging.NewContextHandler が ctx から自動で行います。
func TraceID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tc := resolveTrace(r)
			w.Header().Set(TraceIDHeader, tc.TraceID)
			next.ServeHTTP(w, r.WithContext(logging.WithTrace(r.Context(), tc)))
		})
	}
}

// resolveTrace はリクエストヘッダーからトレース情報を解決し、無ければ新規生成します。
func resolveTrace(r *http.Request) logging.TraceContext {
	if tc, ok := parseTraceparent(r.Header.Get("traceparent")); ok {
		return tc
	}
	if tc, ok := parseCloudTraceContext(r.Header.Get("X-Cloud-Trace-Context")); ok {
		return tc
	}
	return logging.TraceContext{TraceID: logging.NewTraceID()}
}

// parseTraceparent は W3C 形式 "00-<32hex>-<16hex>-<2hex>" をパースします。
func parseTraceparent(h string) (logging.TraceContext, bool) {
	parts := strings.Split(h, "-")
	if len(parts) != 4 {
		return logging.TraceContext{}, false
	}
	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]
	if version != "00" || len(flags) != 2 || !isLowerHex(flags) {
		return logging.TraceContext{}, false
	}
	if !isValidHexID(traceID, 32) || !isValidHexID(spanID, 16) {
		return logging.TraceContext{}, false
	}
	return logging.TraceContext{TraceID: traceID, SpanID: spanID}, true
}

// parseCloudTraceContext は "TRACE_ID/SPAN_ID;o=1" 形式をパースします。
// SPAN_ID は省略可能で、不正な場合は空のままトレース ID のみ採用します。
func parseCloudTraceContext(h string) (logging.TraceContext, bool) {
	if h == "" {
		return logging.TraceContext{}, false
	}
	traceID, rest, hasSpan := strings.Cut(h, "/")
	traceID = strings.ToLower(traceID)
	if !isValidHexID(traceID, 32) {
		return logging.TraceContext{}, false
	}
	tc := logging.TraceContext{TraceID: traceID}
	if hasSpan {
		span, _, _ := strings.Cut(rest, ";")
		if n, err := strconv.ParseUint(span, 10, 64); err == nil && n != 0 {
			tc.SpanID = fmt.Sprintf("%016x", n)
		}
	}
	return tc, true
}

// isValidHexID は s が長さ n の小文字 16 進数で、全て 0 ではないことを返します。
func isValidHexID(s string, n int) bool {
	return len(s) == n && isLowerHex(s) && strings.Trim(s, "0") != ""
}

// isLowerHex は s が空でなく、小文字 16 進数のみで構成されていることを返します。
func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
