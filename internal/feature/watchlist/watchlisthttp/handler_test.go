package watchlisthttp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/watchlisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

const testUserID int64 = 1

// mockUsecase は Usecase インターフェースのモック実装です。
type mockUsecase struct {
	ListUserSymbolsFunc func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error)
	AddSymbolFunc       func(ctx context.Context, userID int64, symbolCode string) error
	RemoveSymbolFunc    func(ctx context.Context, userID int64, symbolCode string) error
	ReorderSymbolsFunc  func(ctx context.Context, userID int64, orderedCodes []string) error
}

func (m *mockUsecase) ListUserSymbols(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
	if m.ListUserSymbolsFunc != nil {
		return m.ListUserSymbolsFunc(ctx, userID)
	}
	return nil, nil
}

func (m *mockUsecase) AddSymbol(ctx context.Context, userID int64, symbolCode string) error {
	if m.AddSymbolFunc != nil {
		return m.AddSymbolFunc(ctx, userID, symbolCode)
	}
	return nil
}

func (m *mockUsecase) RemoveSymbol(ctx context.Context, userID int64, symbolCode string) error {
	if m.RemoveSymbolFunc != nil {
		return m.RemoveSymbolFunc(ctx, userID, symbolCode)
	}
	return nil
}

func (m *mockUsecase) ReorderSymbols(ctx context.Context, userID int64, orderedCodes []string) error {
	if m.ReorderSymbolsFunc != nil {
		return m.ReorderSymbolsFunc(ctx, userID, orderedCodes)
	}
	return nil
}

// newRouter は認証済みユーザーIDを context に注入するミドルウェア付きの chi ルーターを構築します。
func newRouter(t *testing.T, register func(r chi.Router)) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(jwt.WithUserID(req.Context(), testUserID)))
		})
	})
	register(r)
	return r
}

// newRouterNoAuth はユーザーIDを context に注入しない素の chi ルーターを構築します。
// userID 未設定時のフォールバック分岐を検証するために使用します。
func newRouterNoAuth(register func(r chi.Router)) chi.Router {
	r := chi.NewRouter()
	register(r)
	return r
}

func TestWatchlistHandler_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		mockList       func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "success: returns watchlist items",
			mockList: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				assert.Equal(t, testUserID, userID)
				return []watchlist.UserSymbol{
					{ID: 1, UserID: testUserID, SymbolCode: "AAPL", SortKey: 0},
					{ID: 2, UserID: testUserID, SymbolCode: "MSFT", SortKey: 1},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"id":1,"symbol_code":"AAPL","sort_key":0},{"id":2,"symbol_code":"MSFT","sort_key":1}]`,
		},
		{
			name: "success: empty watchlist returns empty array",
			mockList: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return []watchlist.UserSymbol{}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[]`,
		},
		{
			name: "error: usecase returns error",
			mockList: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
				return nil, errors.New("db failure")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{ListUserSymbolsFunc: tt.mockList}
			h := watchlisthttp.NewHandler(mockUC)
			router := newRouter(t, func(r chi.Router) {
				r.Get("/watchlist", h.List)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/watchlist", nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}

func TestWatchlistHandler_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           string
		mockAdd        func(ctx context.Context, userID int64, symbolCode string) error
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "success: symbol added",
			body: `{"symbol_code":"AAPL"}`,
			mockAdd: func(ctx context.Context, userID int64, symbolCode string) error {
				assert.Equal(t, testUserID, userID)
				assert.Equal(t, "AAPL", symbolCode)
				return nil
			},
			expectedStatus: http.StatusCreated,
			expectedBody:   `{"message":"added to watchlist"}`,
		},
		{
			name: "error: symbol not found",
			body: `{"symbol_code":"XXXX"}`,
			mockAdd: func(ctx context.Context, userID int64, symbolCode string) error {
				return watchlist.ErrSymbolNotFound
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   `{"error":"symbol not found"}`,
		},
		{
			name: "error: already in watchlist",
			body: `{"symbol_code":"AAPL"}`,
			mockAdd: func(ctx context.Context, userID int64, symbolCode string) error {
				return watchlist.ErrAlreadyInWatchlist
			},
			expectedStatus: http.StatusConflict,
			expectedBody:   `{"error":"symbol already in watchlist"}`,
		},
		{
			// 空文字・必須等のスキーマ検証はミドルウェアの責務（middleware_test.go）。
			// ここではハンドラ自身の JSON デコード失敗分岐を検証する。
			name:           "error: malformed JSON returns 400",
			body:           `{"symbol_code":`,
			mockAdd:        nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid request"}`,
		},
		{
			name:           "error: symbol code with invalid characters returns 400",
			body:           `{"symbol_code":"AAPL@x"}`,
			mockAdd:        nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
		{
			name: "error: usecase returns internal error",
			body: `{"symbol_code":"AAPL"}`,
			mockAdd: func(ctx context.Context, userID int64, symbolCode string) error {
				return errors.New("db failure")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{AddSymbolFunc: tt.mockAdd}
			h := watchlisthttp.NewHandler(mockUC)
			router := newRouter(t, func(r chi.Router) {
				r.Post("/watchlist", h.Add)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/watchlist", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.JSONEq(t, tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestWatchlistHandler_Remove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		code           string
		mockRemove     func(ctx context.Context, userID int64, symbolCode string) error
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "success: symbol removed",
			code: "AAPL",
			mockRemove: func(ctx context.Context, userID int64, symbolCode string) error {
				assert.Equal(t, testUserID, userID)
				assert.Equal(t, "AAPL", symbolCode)
				return nil
			},
			expectedStatus: http.StatusNoContent,
			expectedBody:   "",
		},
		{
			name: "error: not in watchlist",
			code: "AAPL",
			mockRemove: func(ctx context.Context, userID int64, symbolCode string) error {
				return watchlist.ErrNotInWatchlist
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   `{"error":"symbol not in watchlist"}`,
		},
		{
			name: "error: usecase returns internal error",
			code: "AAPL",
			mockRemove: func(ctx context.Context, userID int64, symbolCode string) error {
				return errors.New("db failure")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
		{
			name:           "error: symbol code with invalid characters returns 400",
			code:           "AAPL%26x",
			mockRemove:     nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
		{
			name:           "error: symbol code longer than 20 characters returns 400",
			code:           "AAAAAAAAAAAAAAAAAAAAA",
			mockRemove:     nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{RemoveSymbolFunc: tt.mockRemove}
			h := watchlisthttp.NewHandler(mockUC)
			router := newRouter(t, func(r chi.Router) {
				r.Delete("/watchlist/{code}", h.Remove)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/watchlist/"+tt.code, nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.JSONEq(t, tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestWatchlistHandler_MissingUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		register func(r chi.Router, h *watchlisthttp.Handler)
	}{
		{
			name:   "List returns 500 when userID is missing",
			method: http.MethodGet,
			path:   "/watchlist",
			register: func(r chi.Router, h *watchlisthttp.Handler) {
				r.Get("/watchlist", h.List)
			},
		},
		{
			name:   "Add returns 500 when userID is missing",
			method: http.MethodPost,
			path:   "/watchlist",
			body:   `{"symbol_code":"AAPL"}`,
			register: func(r chi.Router, h *watchlisthttp.Handler) {
				r.Post("/watchlist", h.Add)
			},
		},
		{
			name:   "Remove returns 500 when userID is missing",
			method: http.MethodDelete,
			path:   "/watchlist/AAPL",
			register: func(r chi.Router, h *watchlisthttp.Handler) {
				r.Delete("/watchlist/{code}", h.Remove)
			},
		},
		{
			name:   "Reorder returns 500 when userID is missing",
			method: http.MethodPut,
			path:   "/watchlist/reorder",
			body:   `{"codes":["AAPL"]}`,
			register: func(r chi.Router, h *watchlisthttp.Handler) {
				r.Put("/watchlist/reorder", h.Reorder)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// usecase が呼ばれないことを保証するため、呼び出し時に失敗するモックを渡す。
			failFunc := func() error { t.Fatal("usecase should not be called when userID is missing"); return nil }
			mockUC := &mockUsecase{
				ListUserSymbolsFunc: func(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error) {
					return nil, failFunc()
				},
				AddSymbolFunc:      func(ctx context.Context, userID int64, symbolCode string) error { return failFunc() },
				RemoveSymbolFunc:   func(ctx context.Context, userID int64, symbolCode string) error { return failFunc() },
				ReorderSymbolsFunc: func(ctx context.Context, userID int64, orderedCodes []string) error { return failFunc() },
			}
			h := watchlisthttp.NewHandler(mockUC)
			router := newRouterNoAuth(func(r chi.Router) {
				tt.register(r, h)
			})

			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = bytes.NewReader([]byte(tt.body))
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.JSONEq(t, `{"error":"internal server error"}`, w.Body.String())
		})
	}
}

func TestWatchlistHandler_Reorder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           string
		mockReorder    func(ctx context.Context, userID int64, orderedCodes []string) error
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "success: watchlist reordered",
			body: `{"codes":["MSFT","AAPL"]}`,
			mockReorder: func(ctx context.Context, userID int64, orderedCodes []string) error {
				assert.Equal(t, testUserID, userID)
				assert.Equal(t, []string{"MSFT", "AAPL"}, orderedCodes)
				return nil
			},
			expectedStatus: http.StatusNoContent,
			expectedBody:   "",
		},
		{
			// 空配列・必須等のスキーマ検証はミドルウェアの責務（middleware_test.go）。
			// ここではハンドラ自身の JSON デコード失敗分岐を検証する。
			name:           "error: malformed JSON returns 400",
			body:           `{"codes":`,
			mockReorder:    nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid request"}`,
		},
		{
			name:           "error: code with invalid characters returns 400",
			body:           `{"codes":["AAPL","MSFT@x"]}`,
			mockReorder:    nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid symbol code"}`,
		},
		{
			name: "error: codes mismatch returns 400",
			body: `{"codes":["AAPL"]}`,
			mockReorder: func(ctx context.Context, userID int64, orderedCodes []string) error {
				return watchlist.ErrReorderCodesMismatch
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"reorder codes do not match watchlist"}`,
		},
		{
			name: "error: usecase returns internal error",
			body: `{"codes":["AAPL"]}`,
			mockReorder: func(ctx context.Context, userID int64, orderedCodes []string) error {
				return errors.New("db failure")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   `{"error":"internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockUC := &mockUsecase{ReorderSymbolsFunc: tt.mockReorder}
			h := watchlisthttp.NewHandler(mockUC)
			router := newRouter(t, func(r chi.Router) {
				r.Put("/watchlist/reorder", h.Reorder)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/watchlist/reorder", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.JSONEq(t, tt.expectedBody, w.Body.String())
			}
		})
	}
}
