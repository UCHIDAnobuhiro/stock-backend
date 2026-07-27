package apispec

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMutationOperationsRequireCookieAndCSRF(t *testing.T) {
	t.Parallel()

	doc, err := Load()
	require.NoError(t, err)

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/v1/watchlist"},
		{method: http.MethodDelete, path: "/v1/watchlist/{code}"},
		{method: http.MethodPut, path: "/v1/watchlist/order"},
		{method: http.MethodPost, path: "/v1/logo/detect"},
		{method: http.MethodPost, path: "/v1/logo/analyze"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			pathItem := doc.Paths.Find(tt.path)
			require.NotNil(t, pathItem)

			operation := pathItem.GetOperation(tt.method)
			require.NotNil(t, operation)
			require.NotNil(t, operation.Security)
			require.Len(t, *operation.Security, 1)

			require.Contains(t, (*operation.Security)[0], "cookieAuth")
			require.Contains(t, (*operation.Security)[0], "csrfToken")
		})
	}
}
