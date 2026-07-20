package logodetection

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"unicode/utf8"
)

const (
	// MaxImageSize は画像アップロードの最大サイズ（10MB）です。
	MaxImageSize = 10 * 1024 * 1024
	// MaxCompanyNameLength は企業名の最大文字数（rune数）です。
	MaxCompanyNameLength = 100
)

//go:embed prompts/analysis.md
var analysisPrompt string

//go:embed prompts/format.md
var analysisFormat string

// AnalysisPromptTemplate は指示文とフォーマットを結合した企業分析のプロンプトテンプレートです。
var AnalysisPromptTemplate = analysisPrompt + "\n## 出力フォーマット\n" + analysisFormat

// validCompanyName は企業名に許可される文字パターンです（英数字・日本語・スペース・中黒）。
var validCompanyName = regexp.MustCompile(`^[\p{L}\p{N} ・\-\.&,'']+$`)

// LogoDetector は画像からロゴを検出するリポジトリインターフェースです。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type LogoDetector interface {
	// DetectLogos は画像バイト列からロゴを検出し、検出結果を返します。
	DetectLogos(ctx context.Context, imageData []byte) ([]DetectedLogo, error)
}

// CompanyAnalyzer は企業分析を生成するリポジトリインターフェースです。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type CompanyAnalyzer interface {
	// Analyze はプロンプトから分析サマリーを生成します。
	Analyze(ctx context.Context, prompt string) (string, error)
}

// usecase はロゴ検出・企業分析のビジネスロジックを提供します。
type usecase struct {
	logoDetector    LogoDetector
	companyAnalyzer CompanyAnalyzer
}

// NewUsecase はusecaseの新しいインスタンスを生成します。
func NewUsecase(ld LogoDetector, ca CompanyAnalyzer) *usecase {
	return &usecase{logoDetector: ld, companyAnalyzer: ca}
}

// DetectLogos は画像データからロゴを検出します。
func (u *usecase) DetectLogos(ctx context.Context, imageData []byte) ([]DetectedLogo, error) {
	if len(imageData) == 0 {
		return nil, ErrEmptyImage
	}
	if len(imageData) > MaxImageSize {
		return nil, ErrImageTooLarge
	}
	return u.logoDetector.DetectLogos(ctx, imageData)
}

// AnalyzeCompany は企業名から分析サマリーを生成します。
func (u *usecase) AnalyzeCompany(ctx context.Context, companyName string) (*CompanyAnalysis, error) {
	if companyName == "" {
		return nil, ErrEmptyCompanyName
	}
	if utf8.RuneCountInString(companyName) > MaxCompanyNameLength {
		return nil, ErrCompanyNameTooLong
	}
	if !validCompanyName.MatchString(companyName) {
		return nil, ErrInvalidCompanyName
	}
	prompt := fmt.Sprintf(AnalysisPromptTemplate, companyName)
	summary, err := u.companyAnalyzer.Analyze(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("company analyzer failed for %q: %w", companyName, err)
	}
	return &CompanyAnalysis{
		CompanyName: companyName,
		Summary:     summary,
	}, nil
}
