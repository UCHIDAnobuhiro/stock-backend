package httpratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okHandler はレートリミットを通過した場合に呼ばれる終端ハンドラーです。
// 呼ばれたかどうかを called に記録します。
func okHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if called != nil {
			*called = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// TestByIP_Allowed はレートリミット内のリクエストがハンドラーまで到達し200を返すことを検証します。
func TestByIP_Allowed(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := time.Minute
	setupEvalMock(mock, "rl:test:ip:192.0.2.1", 1, 0) // allowed=1, count=0

	limiter := NewLimiter(rdb)
	cfg := IPRateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window}

	called := false
	h := ByIP(limiter, cfg)(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "ハンドラーが呼ばれるべき")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestByIP_RateLimited はレートリミット超過時に429とRetry-Afterヘッダーが返され、
// ハンドラーが呼ばれないことを検証します。
func TestByIP_RateLimited(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := time.Minute
	setupEvalMock(mock, "rl:test:ip:192.0.2.1", 0, 10) // allowed=0, count=10 (at limit)

	limiter := NewLimiter(rdb)
	cfg := IPRateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window}

	handlerCalled := false
	h := ByIP(limiter, cfg)(okHandler(&handlerCalled))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.False(t, handlerCalled, "ハンドラーは呼ばれるべきではない")

	// Retry-Afterヘッダーの検証
	assert.Equal(t, "60", w.Header().Get("Retry-After"))

	// レスポンスボディの検証
	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "too many requests", body["error"])

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestByIP_NilRedis_Allowed はRedisクライアントがnilの場合（Policy未指定=FailOpen）に
// ミドルウェアがリクエストを通過させることを検証します。
func TestByIP_NilRedis_Allowed(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := IPRateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: time.Minute}

	called := false
	h := ByIP(limiter, cfg)(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called)
}

// TestByIP_NilRedis_FailClosed_ServiceUnavailable はRedisクライアントがnilかつ
// Policy=FailClosedの場合、ハンドラーを呼ばずに503を返すことを検証します。
func TestByIP_NilRedis_FailClosed_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := IPRateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: time.Minute, Policy: FailClosed}

	called := false
	h := ByIP(limiter, cfg)(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.False(t, called, "ハンドラーは呼ばれるべきではない")
	assert.Empty(t, w.Header().Get("Retry-After"), "Retry-Afterヘッダーは付与しない")

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "service temporarily unavailable", body["error"])
}

// TestByIP_RedisError_FailClosed_ServiceUnavailable はRedisエラー時、Policy=FailClosedの
// 場合にハンドラーを呼ばずに503を返すことを検証します。
func TestByIP_RedisError_FailClosed_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := time.Minute
	setupEvalErrorMock(mock, "rl:test:ip:192.0.2.1", fmt.Errorf("connection refused"))

	limiter := NewLimiter(rdb)
	cfg := IPRateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window, Policy: FailClosed}

	handlerCalled := false
	h := ByIP(limiter, cfg)(okHandler(&handlerCalled))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.False(t, handlerCalled, "ハンドラーは呼ばれるべきではない")
	assert.Empty(t, w.Header().Get("Retry-After"), "Retry-Afterヘッダーは付与しない")

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "service temporarily unavailable", body["error"])

	assert.NoError(t, mock.ExpectationsWereMet())
}
