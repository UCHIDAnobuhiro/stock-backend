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

	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
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
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window, Policy: FailOpen}

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
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window, Policy: FailOpen}

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

// TestByIP_NilRedis_Allowed はRedisクライアントがnilかつPolicy=FailOpenを明示指定した場合に
// ミドルウェアがリクエストを通過させることを検証します。
func TestByIP_NilRedis_Allowed(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: time.Minute, Policy: FailOpen}

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
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: time.Minute, Policy: FailClosed}

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

// TestByIP_DefaultPolicy_FailClosed はRateLimitConfig.Policyを未指定（ゼロ値）のまま
// Redisクライアントがnilの場合に、secure by defaultの方針によりゼロ値がFailClosedとして
// 扱われ、ハンドラーを呼ばずに503を返すことを検証します。Policyの指定漏れが安全側（拒否）に
// 倒れることそのものが主眼です。
func TestByIP_DefaultPolicy_FailClosed(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	// Policyフィールドを意図的に指定しない（ゼロ値のまま）。
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: time.Minute}

	called := false
	h := ByIP(limiter, cfg)(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "Policy未指定でもゼロ値=FailClosedとして拒否されるべき")
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
	cfg := RateLimitConfig{Prefix: "rl:test:ip", Limit: 10, Window: window, Policy: FailClosed}

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

// newUserRequest はcontextに認証済みユーザーIDを注入したリクエストを生成します。
func newUserRequest(userID int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	if userID != 0 {
		req = req.WithContext(jwt.WithUserID(req.Context(), userID))
	}
	return req
}

// TestByUserID_Allowed はレートリミット内のリクエストがハンドラーまで到達し200を返すことを検証します。
func TestByUserID_Allowed(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := 24 * time.Hour
	setupEvalMock(mock, "rl:test:user:1", 1, 0) // allowed=1, count=0

	limiter := NewLimiter(rdb)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: window, Policy: FailOpen}

	called := false
	h := ByUserID(limiter, cfg)(okHandler(&called))

	req := newUserRequest(1)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "ハンドラーが呼ばれるべき")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestByUserID_RateLimited はレートリミット超過時に429とRetry-Afterヘッダーが返され、
// ハンドラーが呼ばれないことを検証します。
func TestByUserID_RateLimited(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := 24 * time.Hour
	setupEvalMock(mock, "rl:test:user:1", 0, 10) // allowed=0, count=10 (at limit)

	limiter := NewLimiter(rdb)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: window, Policy: FailOpen}

	handlerCalled := false
	h := ByUserID(limiter, cfg)(okHandler(&handlerCalled))

	req := newUserRequest(1)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.False(t, handlerCalled, "ハンドラーは呼ばれるべきではない")
	assert.Equal(t, "86400", w.Header().Get("Retry-After"))

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "too many requests", body["error"])

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestByUserID_IndependentCountPerUser はユーザーごとにカウントが独立していることを検証します。
// userID=1が超過状態でも、userID=2は別バケットとして通過することを確認します。
func TestByUserID_IndependentCountPerUser(t *testing.T) {
	t.Parallel()

	rdb, mock := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()

	window := 24 * time.Hour
	setupEvalMock(mock, "rl:test:user:1", 0, 10) // userID=1: 超過
	setupEvalMock(mock, "rl:test:user:2", 1, 0)  // userID=2: 未使用

	limiter := NewLimiter(rdb)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: window, Policy: FailOpen}

	h := ByUserID(limiter, cfg)(okHandler(nil))

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, newUserRequest(1))
	assert.Equal(t, http.StatusTooManyRequests, w1.Code)

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, newUserRequest(2))
	assert.Equal(t, http.StatusOK, w2.Code)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestByUserID_NoUserIDInContext はcontextにユーザーIDが無い場合（AuthRequiredより前段に
// 配置された設定ミス等）に500を返すことを検証します。
func TestByUserID_NoUserIDInContext(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: time.Minute, Policy: FailOpen}

	called := false
	h := ByUserID(limiter, cfg)(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/test", nil) // userIDを注入しない
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.False(t, called, "ハンドラーは呼ばれるべきではない")

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "internal server error", body["error"])
}

// TestByUserID_NilRedis_FailClosed_ServiceUnavailable はRedisクライアントがnilかつ
// Policy=FailClosedの場合、ハンドラーを呼ばずに503を返すことを検証します。
func TestByUserID_NilRedis_FailClosed_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: time.Minute, Policy: FailClosed}

	called := false
	h := ByUserID(limiter, cfg)(okHandler(&called))

	req := newUserRequest(1)
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

// TestByUserID_NilRedis_FailOpen_Allowed はRedisクライアントがnilかつPolicy=FailOpenを
// 明示指定した場合にミドルウェアがリクエストを通過させることを検証します。
func TestByUserID_NilRedis_FailOpen_Allowed(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(nil)
	cfg := RateLimitConfig{Prefix: "rl:test:user", Limit: 10, Window: time.Minute, Policy: FailOpen}

	called := false
	h := ByUserID(limiter, cfg)(okHandler(&called))

	req := newUserRequest(1)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "ハンドラーが呼ばれるべき")
}
