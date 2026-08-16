package di

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/authhttp"
)

// oauthHTTPTimeout は OAuth プロバイダ（Google/GitHub）への HTTP 呼び出しに用いるタイムアウト。
const oauthHTTPTimeout = 10 * time.Second

// ProviderCredentials は OAuth プロバイダ1社分の検証済み認証情報。
type ProviderCredentials struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OAuthConfig は OAuth 機能の検証済み設定。いずれかのプロバイダが設定された場合のみ生成される。
type OAuthConfig struct {
	FrontendURL string
	Google      *ProviderCredentials // 未設定なら nil
	GitHub      *ProviderCredentials // 未設定なら nil
}

// OAuthUserStore は OAuth ユースケースが必要とするユーザー永続化操作をまとめた合成インターフェース。
// NewOAuthUsecase の users / creator 両引数へ同一の実装を渡すために用いる。
type OAuthUserStore interface {
	auth.UserRepository
	auth.OAuthUserCreator
}

// NewOAuthHandler は OAuth 機能一式（プロバイダ・ユースケース・ハンドラー）を組み立てる。
// OAuth は state 保存に Redis を必須とするため、rdb が nil の場合はエラーを返す。
func NewOAuthHandler(
	cfg *OAuthConfig,
	db *sql.DB,
	rdb *redis.Client,
	userStore OAuthUserStore,
	sessions auth.SessionIssuer,
	onUserCreated auth.UserCreatedHook,
	cookies authhttp.SessionCookieConfig,
) (*authhttp.OAuthHandler, error) {
	if rdb == nil {
		return nil, fmt.Errorf("OAuth requires Redis but Redis is unavailable")
	}

	hc := &http.Client{Timeout: oauthHTTPTimeout}
	providers := map[string]auth.OAuthProvider{}
	if cfg.Google != nil {
		providers["google"] = auth.NewGoogleProvider(
			cfg.Google.ClientID,
			cfg.Google.ClientSecret,
			cfg.Google.RedirectURL,
			hc,
		)
	}
	if cfg.GitHub != nil {
		providers["github"] = auth.NewGitHubProvider(
			cfg.GitHub.ClientID,
			cfg.GitHub.ClientSecret,
			cfg.GitHub.RedirectURL,
			hc,
		)
	}

	oauthUC := auth.NewOAuthUsecase(
		userStore,
		auth.NewOAuthAccountRepository(db),
		userStore,
		auth.NewRedisOAuthStateStore(rdb),
		sessions,
		providers,
		onUserCreated,
	)

	return authhttp.NewOAuthHandler(oauthUC, cookies, cfg.FrontendURL), nil
}
