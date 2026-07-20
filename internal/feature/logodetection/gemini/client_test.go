package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
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

// TestGeminiAnalyzer_Analyze は Analyze の正常系・異常系を検証します。
//
// resp == nil を扱うガード節は genai SDK 経由では到達不能（sendRequest がエラー時は必ず
// err を返すため、resp が nil かつ err が nil のケースは発生しない）ためテスト対象外です。
// また ADC（Application Default Credentials）を利用する NewGeminiAnalyzer は
// 実クレデンシャルなしに検証できないため、意図的にテスト対象外としています。
func TestGeminiAnalyzer_Analyze(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		handler     http.HandlerFunc
		wantText    string
		wantErr     bool
		wantErrText string
	}{
		{
			name: "success: single candidate text",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.Path, "generateContent")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"分析結果"}]}}]}`))
			},
			wantText: "分析結果",
		},
		{
			name: "success: multiple text parts are concatenated",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.Path, "generateContent")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"分析"},{"text":"結果"}]}}]}`))
			},
			wantText: "分析結果",
		},
		{
			name: "error: server returns 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":500,"message":"internal error","status":"INTERNAL"}}`))
			},
			wantErr:     true,
			wantErrText: "gemini API request failed",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newTestGeminiAnalyzer(t, tt.handler)

			got, err := g.Analyze(context.Background(), "テストプロンプト")

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantText, got)
		})
	}
}
