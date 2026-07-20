package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/router"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/candleshttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist/symbollisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist/watchlisthttp"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpratelimit"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

const testJWTSecret = "test-jwt-secret-for-router-tests"

// --- スタブ usecase 実装（ミドルウェアチェーンの検証のみが目的のため、戻り値はゼロ値でよい） ---

type stubAuthUsecase struct{}

func (stubAuthUsecase) Signup(_ context.Context, _, _ string) (int64, error) { return 0, nil }
func (stubAuthUsecase) Login(_ context.Context, _, _ string) (string, error) { return "", nil }

type stubOAuthUsecase struct{}

func (stubOAuthUsecase) BeginAuth(_ context.Context, _ string) (string, string, error) {
	return "", "", nil
}
func (stubOAuthUsecase) HandleCallback(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

type stubCandlesUsecase struct{}

func (stubCandlesUsecase) GetCandles(_ context.Context, _, _ string, _ int) ([]candles.Candle, error) {
	return nil, nil
}

type stubSymbolUsecase struct{}

func (stubSymbolUsecase) ListActiveSymbols(_ context.Context) ([]symbollist.Symbol, error) {
	return nil, nil
}

type stubLogoUsecase struct{}

func (stubLogoUsecase) DetectLogos(_ context.Context, _ []byte) ([]logodetection.DetectedLogo, error) {
	return nil, nil
}
func (stubLogoUsecase) AnalyzeCompany(_ context.Context, _ string) (*logodetection.CompanyAnalysis, error) {
	return nil, nil
}

type stubWatchlistUsecase struct{}

func (stubWatchlistUsecase) ListUserSymbols(_ context.Context, _ int64) ([]watchlist.UserSymbol, error) {
	return nil, nil
}
func (stubWatchlistUsecase) AddSymbol(_ context.Context, _ int64, _ string) error { return nil }
func (stubWatchlistUsecase) RemoveSymbol(_ context.Context, _ int64, _ string) error {
	return nil
}
func (stubWatchlistUsecase) ReorderSymbols(_ context.Context, _ int64, _ []string) error {
	return nil
}

// newTestRouter は各テストごとに独立した miniredis インスタンスを使って router.NewRouter を構築します。
// 公開ルートは httpratelimit.FailClosed のため実 Limiter（miniredis 接続）が必要であり、
// OpenAPIValidator は nil だと router.go 側で panic するため no-op を明示的に渡す。
func newTestRouter(t *testing.T, oauth *authhttp.OAuthHandler) http.Handler {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := httpratelimit.NewLimiter(rdb)

	noopValidator := func(next http.Handler) http.Handler { return next }

	h := router.Handlers{
		Auth:      authhttp.NewHandler(stubAuthUsecase{}, limiter, false, testJWTSecret, nil),
		OAuth:     oauth,
		Candles:   candleshttp.NewHandler(stubCandlesUsecase{}),
		Symbol:    symbollisthttp.NewHandler(stubSymbolUsecase{}),
		Logo:      logodetectionhttp.NewHandler(stubLogoUsecase{}),
		Watchlist: watchlisthttp.NewHandler(stubWatchlistUsecase{}),
	}
	cfg := router.Config{
		Limiter:          limiter,
		OpenAPIValidator: noopValidator,
		AllowedOrigins:   []string{"http://localhost:3000"},
		JWTSecret:        testJWTSecret,
	}
	return router.NewRouter(h, cfg)
}

// validToken は保護ルートを通過できる有効な JWT（userID=1）を生成します。
func validToken(t *testing.T) string {
	t.Helper()
	return tokenForUser(t, 1)
}

// tokenForUser は指定した userID で保護ルートを通過できる有効な JWT を生成します。
func tokenForUser(t *testing.T, userID int64) string {
	t.Helper()
	tok, err := jwt.NewGenerator(testJWTSecret, time.Hour).GenerateToken(userID, "user@example.com")
	require.NoError(t, err)
	return tok
}

// TestNewRouter_ProtectedRoutes は保護ルート（JWT必須）がトークン有無・有効性に応じて
// 期待どおりのステータスを返すことを検証します。
func TestNewRouter_ProtectedRoutes(t *testing.T) {
	t.Parallel()

	oauthHandler := authhttp.NewOAuthHandler(stubOAuthUsecase{}, false, "http://localhost:3000")

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"GET candles", http.MethodGet, "/v1/candles/AAPL"},
		{"GET symbols", http.MethodGet, "/v1/symbols"},
		{"POST logo detect", http.MethodPost, "/v1/logo/detect"},
		{"POST logo analyze", http.MethodPost, "/v1/logo/analyze"},
		{"GET watchlist", http.MethodGet, "/v1/watchlist"},
		{"POST watchlist", http.MethodPost, "/v1/watchlist"},
		{"DELETE watchlist item", http.MethodDelete, "/v1/watchlist/AAPL"},
		{"PUT watchlist order", http.MethodPut, "/v1/watchlist/order"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newTestRouter(t, oauthHandler)

			t.Run("error: no token returns 401", func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(tt.method, tt.path, nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
			})

			t.Run("error: garbage bearer token returns 401", func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(tt.method, tt.path, nil)
				req.Header.Set("Authorization", "Bearer invalid")
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
			})

			t.Run("success: valid bearer token passes auth", func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(tt.method, tt.path, nil)
				req.Header.Set("Authorization", "Bearer "+validToken(t))
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				// スタブハンドラーは空ボディ等で 400/500 を返しうるが、401/403 でなければ
				// 認証・CSRF チェックを通過したことの証明として十分（200 は要求しない）。
				assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
				assert.NotEqual(t, http.StatusForbidden, rec.Code)
			})
		})
	}
}

// TestNewRouter_PublicRoutes は認証不要ルートが 401/403 を返さないこと、
// レートリミッターが正しく機能し 503（FailClosed）にならないことを検証します。
func TestNewRouter_PublicRoutes(t *testing.T) {
	t.Parallel()

	oauthHandler := authhttp.NewOAuthHandler(stubOAuthUsecase{}, false, "http://localhost:3000")

	t.Run("success: healthz returns exactly 200", func(t *testing.T) {
		t.Parallel()
		r := newTestRouter(t, oauthHandler)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"POST signup", http.MethodPost, "/v1/signup"},
		{"POST login", http.MethodPost, "/v1/login"},
		{"DELETE logout", http.MethodDelete, "/v1/logout"},
		{"GET oauth begin", http.MethodGet, "/v1/auth/oauth/google"},
		{"GET oauth callback", http.MethodGet, "/v1/auth/oauth/google/callback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newTestRouter(t, oauthHandler)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
			assert.NotEqual(t, http.StatusForbidden, rec.Code)
			assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code)
		})
	}
}

// TestNewRouter_OAuthRoutesOptional は Handlers.OAuth が nil の場合に
// OAuthルートが一切登録されないことを検証します。
func TestNewRouter_OAuthRoutesOptional(t *testing.T) {
	t.Parallel()

	t.Run("success: OAuth nil disables oauth routes", func(t *testing.T) {
		t.Parallel()
		r := newTestRouter(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/google", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestNewRouter_UnknownRoute は未定義パスへのアクセスが 404 になることを検証します。
func TestNewRouter_UnknownRoute(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, authhttp.NewOAuthHandler(stubOAuthUsecase{}, false, "http://localhost:3000"))
	req := httptest.NewRequest(http.MethodGet, "/v1/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestNewRouter_LogoRateLimit は /v1/logo/detect・/v1/logo/analyze がユーザーID単位で
// 1日10回にレートリミットされること、エンドポイントごと・ユーザーごとに独立したバケットで
// カウントされることを検証します。
// 同一ルーターインスタンスへの逐次リクエストでカウント順序を検証するため、
// リクエストを発行するサブテストは t.Parallel() を使わない。
func TestNewRouter_LogoRateLimit(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, nil)

	postLogoDetect := func(t *testing.T, token string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/logo/detect", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	postLogoAnalyze := func(t *testing.T, token string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/logo/analyze", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	tokenUser1 := tokenForUser(t, 1)

	t.Run("success: 10回までは429にならない", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			code := postLogoDetect(t, tokenUser1)
			assert.NotEqual(t, http.StatusTooManyRequests, code, "リクエスト%d回目で429になってはいけない", i+1)
		}
	})

	t.Run("error: 11回目は429になる", func(t *testing.T) {
		code := postLogoDetect(t, tokenUser1)
		assert.Equal(t, http.StatusTooManyRequests, code)
	})

	t.Run("success: 別ユーザーは独立したバケットで429にならない", func(t *testing.T) {
		tokenUser2 := tokenForUser(t, 2)
		code := postLogoDetect(t, tokenUser2)
		assert.NotEqual(t, http.StatusTooManyRequests, code)
	})

	t.Run("success: エンドポイントごとに別バケットのため429にならない", func(t *testing.T) {
		code := postLogoAnalyze(t, tokenUser1)
		assert.NotEqual(t, http.StatusTooManyRequests, code)
	})
}
