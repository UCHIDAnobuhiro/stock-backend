package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
)

// newTestGeminiAnalyzer は httptest サーバーに向いた GeminiAnalyzer を生成するヘルパーです。
// genai SDK は BackendGeminiAPI では APIKey が必須のため、ADC を使わないダミーキーを設定します。
// これにより本番コード（NewGeminiAnalyzer）を変更せずにテストダブルを注入できます。
func newTestGeminiAnalyzer(t *testing.T, handler http.HandlerFunc) *GeminiAnalyzer {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend:    genai.BackendGeminiAPI,
		APIKey:     "test-key",
		HTTPClient: srv.Client(),
		HTTPOptions: genai.HTTPOptions{
			BaseURL: srv.URL,
		},
	})
	require.NoError(t, err)

	return &GeminiAnalyzer{client: client, model: DefaultModel}
}

func TestGeminiAnalyzer_Analyze(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		response    string
		want        *logodetection.CompanyAnalysis
		wantErr     bool
		wantErrText string
	}{
		{
			name:     "success: structured analysis with normalized ticker",
			status:   http.StatusOK,
			response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"company_name\":\" Alphabet Inc. \",\"ticker\":\" googl \",\"summary\":\" 分析結果 \"}"}]}}]}`,
			want: func() *logodetection.CompanyAnalysis {
				ticker := "GOOGL"
				return &logodetection.CompanyAnalysis{CompanyName: "Alphabet Inc.", Ticker: &ticker, Summary: "分析結果"}
			}(),
		},
		{
			name:     "success: nullable ticker",
			status:   http.StatusOK,
			response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"company_name\":\"非上場企業\",\"ticker\":null,\"summary\":\"分析結果\"}"}]}}]}`,
			want:     &logodetection.CompanyAnalysis{CompanyName: "非上場企業", Summary: "分析結果"},
		},
		{
			name:        "error: invalid JSON",
			status:      http.StatusOK,
			response:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"not-json"}]}}]}`,
			wantErr:     true,
			wantErrText: "invalid JSON",
		},
		{
			name:        "error: invalid ticker",
			status:      http.StatusOK,
			response:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"company_name\":\"Alphabet Inc.\",\"ticker\":\"NASDAQ:GOOGL\",\"summary\":\"分析結果\"}"}]}}]}`,
			wantErr:     true,
			wantErrText: "invalid ticker",
		},
		{
			name:        "error: server returns 500",
			status:      http.StatusInternalServerError,
			response:    `{"error":{"code":500,"message":"internal error","status":"INTERNAL"}}`,
			wantErr:     true,
			wantErrText: "gemini API request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				assertStructuredRequest(t, r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.response))
			}

			g := newTestGeminiAnalyzer(t, handler)

			got, err := g.Analyze(context.Background(), "テストプロンプト")

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func assertStructuredRequest(t *testing.T, r *http.Request) {
	t.Helper()
	assert.Contains(t, r.URL.Path, "generateContent")

	var body struct {
		GenerationConfig struct {
			ResponseMIMEType string `json:"responseMimeType"`
			ResponseSchema   struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Nullable *bool `json:"nullable"`
				} `json:"properties"`
			} `json:"responseSchema"`
		} `json:"generationConfig"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	assert.Equal(t, "application/json", body.GenerationConfig.ResponseMIMEType)
	assert.ElementsMatch(t, []string{"company_name", "ticker", "summary"}, body.GenerationConfig.ResponseSchema.Required)
	require.NotNil(t, body.GenerationConfig.ResponseSchema.Properties["ticker"].Nullable)
	assert.True(t, *body.GenerationConfig.ResponseSchema.Properties["ticker"].Nullable)
}
