//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	redisv9 "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/csrf"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("e2e PostgreSQL setup: %v", err)
	}
	os.Exit(code)
}

// TestWatchlistJourneyE2E はブラウザと同じ Cookie / CSRF 操作で、API・認証・DB・Redis を通す。
func TestWatchlistJourneyE2E(t *testing.T) {
	db := dbtest.OpenIsolatedDB(t)
	seedE2ESymbols(t, db)

	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Fatal("TEST_REDIS_ADDR is required for E2E tests")
	}
	rdb := redisv9.NewClient(&redisv9.Options{Addr: redisAddr})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	require.NoError(t, rdb.Ping(t.Context()).Err(), "E2E requires a running Redis")

	cfg := &config.Config{
		Cache: config.CacheConfig{CandlesTTL: time.Hour},
		Server: config.ServerConfig{
			JWTSecret:      strings.Repeat("j", 32),
			PasswordPepper: strings.Repeat("p", 32),
		},
	}
	handler, err := buildRouter(cfg, db, rdb, logodetectionhttp.NewHandler(unusedLogoUsecase{}))
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	status, _ := e2eRequest(t, client, http.MethodGet, server.URL+"/v1/watchlist", "", "")
	require.Equal(t, http.StatusUnauthorized, status, "未ログインではウォッチリストを取得できない")

	email := "watchlist-e2e@example.com"
	password := "test-password-123"
	status, _ = e2eRequest(t, client, http.MethodPost, server.URL+"/v1/signup",
		`{"email":"`+email+`","password":"`+password+`"}`, "")
	require.Equal(t, http.StatusCreated, status, "signup")

	status, _ = e2eRequest(t, client, http.MethodPost, server.URL+"/v1/login",
		`{"email":"`+email+`","password":"`+password+`"}`, "")
	require.Equal(t, http.StatusOK, status, "login")
	csrfToken := e2eCSRFToken(t, jar, server.URL)

	assertE2EWatchlist(t, client, server.URL, []string{"AAPL", "MSFT", "GOOGL"})

	status, _ = e2eRequest(t, client, http.MethodPost, server.URL+"/v1/watchlist",
		`{"symbol_code":"NVDA"}`, csrfToken)
	require.Equal(t, http.StatusCreated, status, "add NVDA")
	assertE2EWatchlist(t, client, server.URL, []string{"AAPL", "MSFT", "GOOGL", "NVDA"})

	status, _ = e2eRequest(t, client, http.MethodPut, server.URL+"/v1/watchlist/order",
		`{"codes":["NVDA","AAPL","MSFT","GOOGL"]}`, csrfToken)
	require.Equal(t, http.StatusNoContent, status, "reorder")
	assertE2EWatchlist(t, client, server.URL, []string{"NVDA", "AAPL", "MSFT", "GOOGL"})

	status, _ = e2eRequest(t, client, http.MethodDelete, server.URL+"/v1/watchlist/MSFT", "", csrfToken)
	require.Equal(t, http.StatusNoContent, status, "remove MSFT")
	assertE2EWatchlist(t, client, server.URL, []string{"NVDA", "AAPL", "GOOGL"})
}

func seedE2ESymbols(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `INSERT INTO symbols (code, name, market, timezone) VALUES
        ('AAPL', 'Apple', 'NASDAQ', 'America/New_York'),
        ('MSFT', 'Microsoft', 'NASDAQ', 'America/New_York'),
        ('GOOGL', 'Alphabet', 'NASDAQ', 'America/New_York'),
        ('NVDA', 'NVIDIA', 'NASDAQ', 'America/New_York')`)
	require.NoError(t, err)
}

func e2eRequest(t *testing.T, client *http.Client, method, address, body, csrfToken string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, address, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		req.Header.Set(csrf.HeaderName, csrfToken)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, data
}

func e2eCSRFToken(t *testing.T, jar http.CookieJar, address string) string {
	t.Helper()
	parsed, err := url.Parse(address + "/v1/watchlist")
	require.NoError(t, err)
	cookies := jar.Cookies(parsed)
	names := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		names[cookie.Name] = cookie.Value
	}
	require.NotEmpty(t, names["auth_token"], "login must issue auth cookie")
	require.NotEmpty(t, names["refresh_token"], "login must issue refresh cookie")
	require.NotEmpty(t, names[csrf.CookieName], "login must issue CSRF cookie")
	return names[csrf.CookieName]
}

func assertE2EWatchlist(t *testing.T, client *http.Client, serverURL string, want []string) {
	t.Helper()
	status, body := e2eRequest(t, client, http.MethodGet, serverURL+"/v1/watchlist", "", "")
	require.Equal(t, http.StatusOK, status, "list watchlist: %s", body)
	var items []struct {
		ID         int64  `json:"id"`
		SymbolCode string `json:"symbol_code"`
		SortKey    int64  `json:"sort_key"`
	}
	require.NoError(t, json.Unmarshal(body, &items))
	require.Len(t, items, len(want))
	for i, item := range items {
		require.Positive(t, item.ID)
		require.Equal(t, want[i], item.SymbolCode, "watchlist order at %d", i)
		if len(want) == 4 { // 並べ替え後は全件の sort_key が要求順に更新される。
			require.Equal(t, int64(i), item.SortKey)
		}
	}
}

type unusedLogoUsecase struct{}

func (unusedLogoUsecase) DetectLogos(context.Context, []byte) ([]logodetection.DetectedLogo, error) {
	return nil, errors.New("logo detection is outside this E2E scenario")
}

func (unusedLogoUsecase) AnalyzeCompany(context.Context, string) (*logodetection.CompanyAnalysis, error) {
	return nil, errors.New("logo analysis is outside this E2E scenario")
}
