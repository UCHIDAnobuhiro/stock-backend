package handler

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupRouter はヘルスチェックエンドポイントを処理するテスト用ハンドラーを生成します。
// Health はメソッドごとの分岐を自身で行うため、全メソッドを単一ハンドラーで処理します。
func setupRouter() http.Handler {
	return http.HandlerFunc(Health)
}

// TestHealth_GET はGETリクエストでJSON形式のステータスレスポンスとCache-Controlヘッダーが返されることを検証します。
func TestHealth_GET(t *testing.T) {
	t.Parallel()

	router := setupRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Check response body
	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", response["status"])
	}

	// Check Cache-Control header
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control 'no-store', got %q", w.Header().Get("Cache-Control"))
	}
}

// TestHealth_HEAD はHEADリクエストで200ステータスとレスポンスボディなしが返されることを検証します。
func TestHealth_HEAD(t *testing.T) {
	t.Parallel()

	router := setupRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/healthz", nil)

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// HEAD should have no body
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body for HEAD request, got %d bytes", w.Body.Len())
	}

	// Check Cache-Control header
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control 'no-store', got %q", w.Header().Get("Cache-Control"))
	}
}

// TestHealth_OPTIONS はOPTIONSリクエストで204ステータスが返されることを検証します。
func TestHealth_OPTIONS(t *testing.T) {
	t.Parallel()

	router := setupRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// Check Cache-Control header
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control 'no-store', got %q", w.Header().Get("Cache-Control"))
	}
}

// TestHealth_POST はPOSTリクエストでJSON形式のステータスレスポンスが返されることを検証します。
func TestHealth_POST(t *testing.T) {
	t.Parallel()

	router := setupRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	router.ServeHTTP(w, req)

	// Default case returns JSON
	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", response["status"])
	}
}

// TestHealth_AllMethods_CacheControl はすべてのHTTPメソッドでCache-Control: no-storeヘッダーが設定されることを検証します。
func TestHealth_AllMethods_CacheControl(t *testing.T) {
	t.Parallel()

	methods := []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
	}

	router := setupRouter()

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/healthz", nil)

			router.ServeHTTP(w, req)

			// All methods should have Cache-Control header
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("expected Cache-Control 'no-store', got %q", w.Header().Get("Cache-Control"))
			}
		})
	}
}

// TestHealth_ResponseStatus は各HTTPメソッドに対して正しいステータスコードが返されることを検証します。
func TestHealth_ResponseStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method         string
		expectedStatus int
	}{
		{http.MethodGet, http.StatusOK},
		{http.MethodHead, http.StatusOK},
		{http.MethodOptions, http.StatusNoContent},
		{http.MethodPost, http.StatusOK},
		{http.MethodPut, http.StatusOK},
		{http.MethodDelete, http.StatusOK},
	}

	router := setupRouter()

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "/healthz", nil)

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}
