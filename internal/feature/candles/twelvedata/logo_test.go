package twelvedata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTwelveDataMarket_GetLogoURL_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logo" {
			t.Errorf("expected path /logo, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "AAPL" {
			t.Errorf("expected symbol AAPL, got %s", r.URL.Query().Get("symbol"))
		}
		if r.Header.Get("Authorization") != "apikey test-key" {
			t.Errorf("expected Authorization header 'apikey test-key', got %s", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("apikey") != "" {
			t.Errorf("expected apikey not present in query, got %s", r.URL.Query().Get("apikey"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"meta":{"symbol":"AAPL"},"url":"https://api.twelvedata.com/logo/apple.com"}`))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "test-key", BaseURL: server.URL}, server.Client())

	logoURL, err := market.GetLogoURL(context.Background(), "AAPL")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logoURL != "https://api.twelvedata.com/logo/apple.com" {
		t.Errorf("unexpected logo url: %s", logoURL)
	}
}

func TestTwelveDataMarket_GetLogoURL_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "test-key", BaseURL: server.URL}, server.Client())

	_, err := market.GetLogoURL(context.Background(), "AAPL")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "twelvedata http") {
		t.Errorf("expected HTTP error, got %v", err)
	}
}

func TestTwelveDataMarket_GetLogoURL_APIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"error","message":"Invalid API key"}`))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "invalid-key", BaseURL: server.URL}, server.Client())

	_, err := market.GetLogoURL(context.Background(), "AAPL")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("expected API error message, got %v", err)
	}
}

func TestTwelveDataMarket_GetLogoURL_InvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "test-key", BaseURL: server.URL}, server.Client())

	if _, err := market.GetLogoURL(context.Background(), "AAPL"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTwelveDataMarket_GetLogoURL_InvalidURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		logoURL string
	}{
		{name: "http scheme", logoURL: "http://api.twelvedata.com/logo/apple.com"},
		{name: "javascript scheme", logoURL: "javascript:alert(1)"},
		{name: "scheme relative", logoURL: "//api.twelvedata.com/logo/apple.com"},
		{name: "missing host", logoURL: "https:///logo/apple.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"meta":{"symbol":"AAPL"},"url":"` + tt.logoURL + `"}`))
			}))
			defer server.Close()

			market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "test-key", BaseURL: server.URL}, server.Client())

			_, err := market.GetLogoURL(context.Background(), "AAPL")

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "invalid logo url") {
				t.Errorf("expected invalid logo url error, got %v", err)
			}
		})
	}
}

func TestTwelveDataMarket_GetLogoURL_EmptyURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"meta":{"symbol":"AAPL"},"url":"   "}`))
	}))
	defer server.Close()

	market := NewTwelveDataMarket(Config{TwelveDataAPIKey: "test-key", BaseURL: server.URL}, server.Client())

	_, err := market.GetLogoURL(context.Background(), "AAPL")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "empty logo url") {
		t.Errorf("expected empty logo url error, got %v", err)
	}
}
