// Package gemini はGoogle Gemini APIを使用した企業分析クライアントを提供します。
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/genai"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
)

const (
	// DefaultModel はGemini APIのデフォルトモデルです。
	DefaultModel = "gemini-3.5-flash-lite"
)

var tickerPattern = regexp.MustCompile(`^[A-Z0-9._-]{1,20}$`)

var companyAnalysisSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"company_name": {
			Type:        genai.TypeString,
			Description: "ブランド名ではなく、分析対象となる企業の正式名称",
		},
		"ticker": {
			Type:        genai.TypeString,
			Nullable:    new(true),
			Pattern:     `^[A-Za-z0-9._-]{1,20}$`,
			Description: "主要な上場銘柄のティッカーシンボル。不明または非上場の場合はnull",
		},
		"summary": {
			Type:        genai.TypeString,
			Description: "指定されたMarkdown形式の企業分析サマリー",
		},
	},
	Required:         []string{"company_name", "ticker", "summary"},
	PropertyOrdering: []string{"company_name", "ticker", "summary"},
}

type generatedCompanyAnalysis struct {
	CompanyName string  `json:"company_name"`
	Ticker      *string `json:"ticker"`
	Summary     string  `json:"summary"`
}

// GeminiAnalyzer はGoogle Gemini APIを使用して企業分析を生成します。
type GeminiAnalyzer struct {
	client *genai.Client
	model  string
}

// GeminiAnalyzerがCompanyAnalyzerを実装していることをコンパイル時に検証します。
var _ logodetection.CompanyAnalyzer = (*GeminiAnalyzer)(nil)

// NewGeminiAnalyzer はADCを使用してGeminiAnalyzerの新しいインスタンスを生成します。
// 環境変数 GOOGLE_GENAI_USE_VERTEXAI, GOOGLE_CLOUD_PROJECT, GOOGLE_CLOUD_LOCATION が必要です。
func NewGeminiAnalyzer(ctx context.Context) (*GeminiAnalyzer, error) {
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}
	return &GeminiAnalyzer{client: client, model: DefaultModel}, nil
}

// Analyze はプロンプトを使用して構造化された企業分析を生成します。
func (g *GeminiAnalyzer) Analyze(ctx context.Context, prompt string) (*logodetection.CompanyAnalysis, error) {
	resp, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   companyAnalysisSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini API request failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("gemini API returned nil response")
	}

	var generated generatedCompanyAnalysis
	if err := json.Unmarshal([]byte(resp.Text()), &generated); err != nil {
		return nil, fmt.Errorf("gemini API returned invalid JSON: %w", err)
	}

	companyName := strings.TrimSpace(generated.CompanyName)
	if companyName == "" {
		return nil, fmt.Errorf("gemini API returned empty company_name")
	}
	summary := strings.TrimSpace(generated.Summary)
	if summary == "" {
		return nil, fmt.Errorf("gemini API returned empty summary")
	}

	ticker, err := normalizeTicker(generated.Ticker)
	if err != nil {
		return nil, err
	}

	return &logodetection.CompanyAnalysis{
		CompanyName: companyName,
		Ticker:      ticker,
		Summary:     summary,
	}, nil
}

func normalizeTicker(ticker *string) (*string, error) {
	if ticker == nil {
		return nil, nil
	}

	normalized := strings.ToUpper(strings.TrimSpace(*ticker))
	if normalized == "" {
		return nil, nil
	}
	if !tickerPattern.MatchString(normalized) {
		return nil, fmt.Errorf("gemini API returned invalid ticker %q", *ticker)
	}
	return &normalized, nil
}
