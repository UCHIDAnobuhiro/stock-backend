package apispec

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

var publicMutations = map[string]struct{}{
	"POST /v1/signup":   {},
	"POST /v1/login":    {},
	"DELETE /v1/logout": {},
}

const refreshMutation = "POST /v1/auth/refresh"

func TestAllMutationOperationsRequireCSRF(t *testing.T) {
	t.Parallel()

	doc, err := Load()
	require.NoError(t, err)

	for path, pathItem := range doc.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			method = strings.ToUpper(method)
			switch method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				continue
			}

			operationKey := method + " " + path
			if _, ok := publicMutations[operationKey]; ok {
				continue
			}

			t.Run(operationKey, func(t *testing.T) {
				require.NotNil(t, operation.Security)
				if operationKey == refreshMutation {
					require.Len(t, *operation.Security, 1)
					require.True(t, hasExactSecurityRequirement(*operation.Security, "refreshCookie", "csrfToken"))
					return
				}
				require.Len(t, *operation.Security, 2)
				require.True(t, hasExactSecurityRequirement(*operation.Security, "cookieAuth", "csrfToken"))
				require.True(t, hasExactSecurityRequirement(*operation.Security, "bearerAuth"))
			})
		}
	}
}

func TestSecuritySchemes(t *testing.T) {
	t.Parallel()

	doc, err := Load()
	require.NoError(t, err)
	require.NotNil(t, doc.Components)

	tests := []struct {
		name         string
		schemeType   string
		in           string
		headerName   string
		scheme       string
		bearerFormat string
	}{
		{name: "cookieAuth", schemeType: "apiKey", in: "cookie", headerName: "auth_token"},
		{name: "refreshCookie", schemeType: "apiKey", in: "cookie", headerName: "refresh_token"},
		{name: "csrfToken", schemeType: "apiKey", in: "header", headerName: "X-CSRF-Token"},
		{name: "bearerAuth", schemeType: "http", scheme: "bearer", bearerFormat: "JWT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schemeRef, ok := doc.Components.SecuritySchemes[tt.name]
			require.True(t, ok)
			require.NotNil(t, schemeRef)
			require.NotNil(t, schemeRef.Value)
			require.Equal(t, tt.schemeType, schemeRef.Value.Type)
			require.Equal(t, tt.in, schemeRef.Value.In)
			require.Equal(t, tt.headerName, schemeRef.Value.Name)
			require.Equal(t, tt.scheme, schemeRef.Value.Scheme)
			require.Equal(t, tt.bearerFormat, schemeRef.Value.BearerFormat)
		})
	}
}

func hasExactSecurityRequirement(requirements openapi3.SecurityRequirements, names ...string) bool {
	for _, requirement := range requirements {
		if len(requirement) != len(names) {
			continue
		}

		matches := true
		for _, name := range names {
			if _, ok := requirement[name]; !ok {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
