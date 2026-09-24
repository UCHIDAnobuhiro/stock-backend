//go:build e2e

package e2e_test

import (
	"encoding/json"
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

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/router"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/symbollisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/watchlisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/csrf"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/openapivalidate"
)

const testJWTSecret = "watchlist-e2e-jwt-secret-at-least-32-bytes"

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

// TestE2EWatchlist は登録から認証済みの追加・削除まで、公開HTTP APIを通して検証する。
func TestE2EWatchlist(t *testing.T) {
	server, client := newWatchlistServer(t)
	watchlistURL := server.URL + "/v1/watchlist"

	unauthorized := request(t, client, http.MethodGet, watchlistURL, "", "")
	require.Equal(t, http.StatusUnauthorized, unauthorized.status, string(unauthorized.body))

	credentials := `{"email":"watchlist-e2e@example.com","password":"e2e-test-password-123"}`
	signup := request(t, client, http.MethodPost, server.URL+"/v1/signup", credentials, "")
	require.Equal(t, http.StatusCreated, signup.status, string(signup.body))
	login := request(t, client, http.MethodPost, server.URL+"/v1/login", credentials, "")
	require.Equal(t, http.StatusOK, login.status, string(login.body))

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	var csrfToken string
	for _, cookie := range client.Jar.Cookies(serverURL) {
		if cookie.Name == csrf.CookieName {
			csrfToken = cookie.Value
		}
	}
	require.NotEmpty(t, csrfToken, "login must issue a CSRF cookie")

	// サインアップ時に登録される既定銘柄も実DBから一覧取得できることを確認する。
	assertWatchlist(t, client, watchlistURL, []string{"AAPL", "MSFT", "GOOGL"})

	addBody := `{"symbol_code":"TSLA"}`
	forbidden := request(t, client, http.MethodPost, watchlistURL, addBody, "")
	require.Equal(t, http.StatusForbidden, forbidden.status, string(forbidden.body))
	require.JSONEq(t, `{"error":"csrf token mismatch"}`, string(forbidden.body))
	assertWatchlist(t, client, watchlistURL, []string{"AAPL", "MSFT", "GOOGL"})

	added := request(t, client, http.MethodPost, watchlistURL, addBody, csrfToken)
	require.Equal(t, http.StatusCreated, added.status, string(added.body))
	assertWatchlist(t, client, watchlistURL, []string{"AAPL", "MSFT", "GOOGL", "TSLA"})

	removed := request(t, client, http.MethodDelete, watchlistURL+"/TSLA", "", csrfToken)
	require.Equal(t, http.StatusNoContent, removed.status, string(removed.body))
	assertWatchlist(t, client, watchlistURL, []string{"AAPL", "MSFT", "GOOGL"})
}

func newWatchlistServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	db := dbtest.OpenIsolatedDB(t)
	_, err := db.ExecContext(t.Context(), `INSERT INTO symbols (code, name, market, timezone) VALUES
		('AAPL', 'Apple', 'NASDAQ', 'America/New_York'),
		('MSFT', 'Microsoft', 'NASDAQ', 'America/New_York'),
		('GOOGL', 'Alphabet', 'NASDAQ', 'America/New_York'),
		('TSLA', 'Tesla', 'NASDAQ', 'America/New_York')`)
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := httpratelimit.NewLimiter(rdb)
	blacklist := jwt.NewBlacklist(rdb)
	userRepo := auth.NewUserRepository(db)
	symbolRepo := symbollist.NewRepository(db)
	watchlistUC := watchlist.NewUsecase(watchlist.NewRepository(db), symbolRepo)
	sessions := auth.NewSessionService(
		jwt.NewGenerator(testJWTSecret, jwt.DefaultTokenTTL),
		auth.NewRefreshSessionRepository(db),
		auth.DefaultRefreshTokenTTL,
	)
	authUC := auth.NewUsecase(userRepo, sessions, "watchlist-e2e-password-pepper")
	validator, err := openapivalidate.New()
	require.NoError(t, err)

	// watchlist 専用の E2E 構成。認証と watchlist の配線は cmd/api/main.go に合わせているため、
	// 本番の配線を変更した際はここも確認する。対象外のルートは呼ばず、外部 API クライアントを
	// 起動しないため、それらの usecase は nil で登録する。
	handlers := router.Handlers{
		Auth:      authhttp.NewHandler(authUC, limiter, authhttp.SessionCookieConfig{}, testJWTSecret, blacklist, watchlistUC),
		Candles:   candleshttp.NewHandler(nil),
		Symbol:    symbollisthttp.NewHandler(nil),
		Logo:      logodetectionhttp.NewHandler(nil),
		Watchlist: watchlisthttp.NewHandler(watchlistUC),
	}
	srv := httptest.NewServer(router.NewRouter(handlers, router.Config{
		Limiter:          limiter,
		OpenAPIValidator: validator,
		JWTSecret:        testJWTSecret,
		Blacklist:        blacklist,
	}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	return srv, client
}

type httpResult struct {
	status int
	body   []byte
}

func request(t *testing.T, client *http.Client, method, target, payload, csrfToken string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, strings.NewReader(payload))
	require.NoError(t, err)
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		req.Header.Set(csrf.HeaderName, csrfToken)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return httpResult{status: resp.StatusCode, body: body}
}

func assertWatchlist(t *testing.T, client *http.Client, target string, expectedCodes []string) {
	t.Helper()
	result := request(t, client, http.MethodGet, target, "", "")
	require.Equal(t, http.StatusOK, result.status, string(result.body))
	var items []api.WatchlistItem
	require.NoError(t, json.Unmarshal(result.body, &items))
	require.Len(t, items, len(expectedCodes))
	for i, item := range items {
		require.Positive(t, item.Id)
		require.Equal(t, expectedCodes[i], item.SymbolCode)
		require.Equal(t, i, item.SortKey)
	}
}
