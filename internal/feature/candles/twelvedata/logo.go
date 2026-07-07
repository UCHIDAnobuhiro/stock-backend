package twelvedata

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

type logoResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

// GetLogoURL はTwelve DataのLogo endpointから株式向けロゴURLを取得します。
func (t *TwelveDataMarket) GetLogoURL(ctx context.Context, symbol string) (string, error) {
	q := url.Values{}
	q.Set("symbol", symbol)

	u := fmt.Sprintf("%s/logo?%s", t.cfg.BaseURL, q.Encode())
	res, err := t.doRequestWithRetry(ctx, http.MethodGet, u)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
		}
	}()

	var body logoResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Status == "error" {
		return "", fmt.Errorf("twelvedata: %s", body.Message)
	}
	logoURL := strings.TrimSpace(body.URL)
	if logoURL == "" {
		return "", fmt.Errorf("twelvedata: empty logo url for %q", symbol)
	}
	if err := validateLogoURL(logoURL); err != nil {
		return "", fmt.Errorf("twelvedata: invalid logo url for %q: %w", symbol, err)
	}
	return logoURL, nil
}

// validateLogoURL は外部APIが返したロゴURLを検証します。
// 取得したURLはDBに保存されフロントエンドへ配信されるため、
// https以外のスキーム（javascript: 等）やホスト欠落のURLを拒否します。
func validateLogoURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}
