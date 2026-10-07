//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestE2EAuthPasswordBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		password string
		status   int
	}{
		{"eleven Unicode characters", strings.Repeat("あ", 11), http.StatusBadRequest},
		{"six supplementary characters", strings.Repeat("🔑", 6), http.StatusBadRequest},
		{"twelve supplementary characters", strings.Repeat("🔑", 12), http.StatusCreated},
		{"ASCII at byte limit", strings.Repeat("a", 1024), http.StatusCreated},
		{"multibyte at byte limit", strings.Repeat("🔑", 256), http.StatusCreated},
		{"ASCII above byte limit", strings.Repeat("a", 1025), http.StatusBadRequest},
		{"multibyte above byte limit", strings.Repeat("🔑", 256) + "a", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := newWatchlistServer(t)
			body, err := json.Marshal(map[string]string{
				"email": "password-boundary@example.com", "password": tt.password,
			})
			require.NoError(t, err)
			signup := request(t, client, http.MethodPost, server.URL+"/v1/signup", string(body), "")
			require.Equal(t, tt.status, signup.status, string(signup.body))
			if tt.status == http.StatusBadRequest {
				require.JSONEq(t, `{"error":"invalid request"}`, string(signup.body))
				return
			}

			login := request(t, client, http.MethodPost, server.URL+"/v1/login", string(body), "")
			require.Equal(t, http.StatusOK, login.status, string(login.body))
			assertWatchlist(t, client, server.URL+"/v1/watchlist", []string{"AAPL", "MSFT", "GOOGL"})
		})
	}
}
