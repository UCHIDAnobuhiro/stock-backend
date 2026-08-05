package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"
)

// GitHubProvider はOAuthProviderインターフェースのGitHub実装です。
type GitHubProvider struct {
	cfg       *oauth2.Config
	hc        *http.Client
	emailsURL string
	userURL   string
}

var _ OAuthProvider = (*GitHubProvider)(nil)

// NewGitHubProvider はGitHubProviderの新しいインスタンスを生成します。
func NewGitHubProvider(clientID, clientSecret, redirectURL string, hc *http.Client) *GitHubProvider {
	return &GitHubProvider{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"user:email"},
			Endpoint:     githuboauth.Endpoint,
		},
		hc:        hc,
		emailsURL: "https://api.github.com/user/emails",
		userURL:   "https://api.github.com/user",
	}
}

// AuthorizationURL はGitHub認可URLを返します。
// GitHubのOAuth AppはPKCEをサポートしないためcodeChallengeは使用しません。
// stateによるCSRF保護とRedisでのstate管理でセキュリティを確保します。
func (p *GitHubProvider) AuthorizationURL(state, _ string) string {
	return p.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// ExchangeCode はauthorization codeをユーザー情報に交換します。
// GitHub APIの /user/emails で検証済みプライマリメールを、/user で数値IDを取得します。
func (p *GitHubProvider) ExchangeCode(ctx context.Context, code, _ string) (*OAuthUserInfo, error) {
	// oauth2ライブラリはcontextのoauth2.HTTPClientキー経由でHTTPクライアントを取得するため、
	// ここで注入しないとp.hcのタイムアウトがトークン交換に適用されない。
	if p.hc != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, p.hc)
	}
	tok, err := p.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github: code exchange failed: %w", err)
	}

	email, err := p.fetchPrimaryEmail(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}

	uid, err := p.fetchUserID(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}

	return &OAuthUserInfo{ProviderUID: uid, Email: email}, nil
}

func (p *GitHubProvider) fetchPrimaryEmail(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.emailsURL, nil)
	if err != nil {
		return "", fmt.Errorf("github: failed to build emails request: %w", err)
	}
	p.setGitHubHeaders(req, token)

	resp, err := p.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: emails request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: emails API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("github: failed to read emails response: %w", err)
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", fmt.Errorf("github: failed to parse emails: %w", err)
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", ErrOAuthEmailUnavailable
}

func (p *GitHubProvider) fetchUserID(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.userURL, nil)
	if err != nil {
		return "", fmt.Errorf("github: failed to build user request: %w", err)
	}
	p.setGitHubHeaders(req, token)

	resp, err := p.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: user request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: user API returned %d", resp.StatusCode)
	}

	var u struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", fmt.Errorf("github: failed to parse user: %w", err)
	}
	if u.ID == 0 {
		return "", fmt.Errorf("github: user API returned invalid ID")
	}
	return fmt.Sprintf("%d", u.ID), nil
}

func (p *GitHubProvider) setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}
