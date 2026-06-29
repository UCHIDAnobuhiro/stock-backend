package openapivalidate_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/openapivalidate"
)

// newValidatedRouter は OpenAPI バリデーションミドルウェアを /v1 に適用したルーターを返す。
// 各ルートは到達したら 200 を返すだけのダミーハンドラーに繋ぐ。
// （バリデーション通過 = 200、バリデーション失敗 = ミドルウェアが 400 を返す）
func newValidatedRouter(t *testing.T) http.Handler {
	t.Helper()

	validator, err := openapivalidate.New()
	require.NoError(t, err)

	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(validator)
		r.Post("/signup", ok)
		r.Post("/login", ok)
		r.Post("/logo/analyze", ok)
		r.Post("/logo/detect", ok)
		r.Post("/watchlist", ok)
		r.Put("/watchlist/order", ok)
		r.Get("/symbols", ok)
		r.Get("/candles/{code}", ok)
	})
	return r
}

func TestMiddleware_RequestValidation(t *testing.T) {
	t.Parallel()

	router := newValidatedRouter(t)

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		expectedStatus int
	}{
		// --- signup ---
		{"signup: 正常", http.MethodPost, "/v1/signup", `{"email":"test@example.com","password":"password12345"}`, http.StatusOK},
		{"signup: email 形式不正", http.MethodPost, "/v1/signup", `{"email":"invalid-email","password":"password12345"}`, http.StatusBadRequest},
		{"signup: password が短い(<12)", http.MethodPost, "/v1/signup", `{"email":"test@example.com","password":"short"}`, http.StatusBadRequest},
		{"signup: email 欠落", http.MethodPost, "/v1/signup", `{"password":"password12345"}`, http.StatusBadRequest},
		{"signup: 不正な JSON", http.MethodPost, "/v1/signup", `{`, http.StatusBadRequest},

		// --- login ---
		{"login: 正常", http.MethodPost, "/v1/login", `{"email":"test@example.com","password":"x"}`, http.StatusOK},
		{"login: password 欠落", http.MethodPost, "/v1/login", `{"email":"test@example.com"}`, http.StatusBadRequest},
		{"login: password 空文字", http.MethodPost, "/v1/login", `{"email":"test@example.com","password":""}`, http.StatusBadRequest},

		// --- logo/analyze ---
		{"analyze: 正常", http.MethodPost, "/v1/logo/analyze", `{"company_name":"任天堂"}`, http.StatusOK},
		{"analyze: company_name 欠落", http.MethodPost, "/v1/logo/analyze", `{}`, http.StatusBadRequest},
		{"analyze: company_name 空文字", http.MethodPost, "/v1/logo/analyze", `{"company_name":""}`, http.StatusBadRequest},

		// --- watchlist add ---
		{"add: 正常", http.MethodPost, "/v1/watchlist", `{"symbol_code":"AAPL"}`, http.StatusOK},
		{"add: symbol_code 空文字", http.MethodPost, "/v1/watchlist", `{"symbol_code":""}`, http.StatusBadRequest},
		{"add: symbol_code 不正文字", http.MethodPost, "/v1/watchlist", `{"symbol_code":"AAPL@x"}`, http.StatusBadRequest},
		{"add: symbol_code 長すぎ", http.MethodPost, "/v1/watchlist", `{"symbol_code":"AAAAAAAAAAAAAAAAAAAAA"}`, http.StatusBadRequest},

		// --- watchlist reorder ---
		{"reorder: 正常", http.MethodPut, "/v1/watchlist/order", `{"codes":["AAPL","MSFT"]}`, http.StatusOK},
		{"reorder: codes 空配列", http.MethodPut, "/v1/watchlist/order", `{"codes":[]}`, http.StatusBadRequest},
		{"reorder: codes 内に不正文字", http.MethodPut, "/v1/watchlist/order", `{"codes":["AAPL","MSFT@x"]}`, http.StatusBadRequest},

		// --- candles ---
		{"candles: 正常", http.MethodGet, "/v1/candles/AAPL?interval=1week&outputsize=10", "", http.StatusOK},
		{"candles: クエリ省略", http.MethodGet, "/v1/candles/AAPL", "", http.StatusOK},
		{"candles: outputsize 上限超過", http.MethodGet, "/v1/candles/AAPL?outputsize=10000", "", http.StatusBadRequest},
		{"candles: outputsize 0", http.MethodGet, "/v1/candles/AAPL?outputsize=0", "", http.StatusBadRequest},
		{"candles: outputsize 整数以外", http.MethodGet, "/v1/candles/AAPL?outputsize=abc", "", http.StatusBadRequest},
		{"candles: interval 未対応値", http.MethodGet, "/v1/candles/AAPL?interval=3day", "", http.StatusBadRequest},
		// 空文字（?interval=）は kin-openapi v0.140.0 以降、enum 検証の対象となり 400。
		// （v0.133.0 では未指定扱いで通過していた。handler 側にも空文字拒否があり多層防御となる）
		{"candles: interval 空文字はミドルウェアで拒否", http.MethodGet, "/v1/candles/AAPL?interval=", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			// 保護ルートは spec で X-CSRF-Token ヘッダーを必須宣言しているため常に付与する
			// （実運用では csrf ミドルウェアが先に検証し、ここには到達済みの値が渡る）。
			req.Header.Set("X-CSRF-Token", "dummy-csrf-token")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusBadRequest {
				assert.JSONEq(t, `{"error":"invalid request"}`, w.Body.String())
			}
		})
	}
}

// TestMiddleware_SkipsMultipart は multipart の logo/detect が検証スキップされることを確認する。
// （JSON でない multipart ボディでも 400 にならず、後続ハンドラに到達して 200 になる）
func TestMiddleware_SkipsMultipart(t *testing.T) {
	t.Parallel()

	router := newValidatedRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logo/detect", strings.NewReader("not-a-multipart-body"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
