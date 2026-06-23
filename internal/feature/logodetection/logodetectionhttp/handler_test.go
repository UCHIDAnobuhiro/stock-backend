package logodetectionhttp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection/logodetectionhttp"
)

// mockUsecase はUsecaseインターフェースのモック実装です。
type mockUsecase struct {
	DetectLogosFunc    func(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error)
	AnalyzeCompanyFunc func(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error)
}

func (m *mockUsecase) DetectLogos(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error) {
	return m.DetectLogosFunc(ctx, imageData)
}

func (m *mockUsecase) AnalyzeCompany(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error) {
	return m.AnalyzeCompanyFunc(ctx, companyName)
}

// createMultipartRequest はテスト用のマルチパートリクエストを生成するヘルパー関数です。
func createMultipartRequest(t *testing.T, fieldName, fileName string, content []byte) (*http.Request, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("failed to copy content: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/logo/detect", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req, writer.FormDataContentType()
}

func TestLogoDetectionHandler_DetectLogos(t *testing.T) {
	tests := []struct {
		name           string
		setupRequest   func(t *testing.T) *http.Request
		mockFunc       func(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "success: logos detected",
			setupRequest: func(t *testing.T) *http.Request {
				req, _ := createMultipartRequest(t, "image", "test.jpg", []byte("fake-image"))
				return req
			},
			mockFunc: func(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error) {
				return []logodetection.DetectedLogo{
					{Name: "Apple", Confidence: 0.95},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `[{"name":"Apple","confidence":0.95}]`,
		},
		{
			name: "error: no image field",
			setupRequest: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/logo/detect", nil)
			},
			mockFunc:       nil,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"画像ファイルが必要です"}`,
		},
		{
			name: "error: usecase returns error",
			setupRequest: func(t *testing.T) *http.Request {
				req, _ := createMultipartRequest(t, "image", "test.jpg", []byte("fake-image"))
				return req
			},
			mockFunc: func(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error) {
				return nil, errors.New("vision API error")
			},
			expectedStatus: http.StatusBadGateway,
			expectedBody:   `{"error":"ロゴ検出に失敗しました"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockUC := &mockUsecase{
				DetectLogosFunc: tt.mockFunc,
			}

			h := logodetectionhttp.NewHandler(mockUC)

			w := httptest.NewRecorder()
			req := tt.setupRequest(t)

			h.DetectLogos(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}

func TestLogoDetectionHandler_AnalyzeCompany(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    string
		mockFunc       func(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:        "success: analysis generated",
			requestBody: `{"company_name":"任天堂"}`,
			mockFunc: func(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error) {
				assert.Equal(t, "任天堂", companyName)
				return &logodetection.CompanyAnalysis{
					CompanyName: "任天堂",
					Summary:     "任天堂の強みは...",
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"company_name":"任天堂","summary":"任天堂の強みは..."}`,
		},
		// 注: company_name の必須・空文字チェックは OpenAPI バリデーションミドルウェアの
		// 責務に移行したため、その検証は middleware_test.go で実施する。
		// ここではハンドラ自身の JSON デコード失敗分岐のみを検証する。
		{
			name:           "error: invalid json",
			requestBody:    `invalid`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"error":"invalid request"}`,
		},
		{
			name:        "error: usecase returns error",
			requestBody: `{"company_name":"テスト企業"}`,
			mockFunc: func(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error) {
				return nil, errors.New("gemini API error")
			},
			expectedStatus: http.StatusBadGateway,
			expectedBody:   `{"error":"企業分析に失敗しました"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockUC := &mockUsecase{
				AnalyzeCompanyFunc: tt.mockFunc,
			}

			h := logodetectionhttp.NewHandler(mockUC)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/logo/analyze", strings.NewReader(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")

			h.AnalyzeCompany(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}
