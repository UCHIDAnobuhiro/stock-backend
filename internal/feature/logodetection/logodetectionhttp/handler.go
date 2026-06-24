package logodetectionhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// Usecase はロゴ検出・企業分析のユースケースインターフェースを定義します。
// Goの慣例に従い、インターフェースは利用者（handler）側で定義します。
type Usecase interface {
	DetectLogos(ctx context.Context, imageData []byte) ([]logodetection.DetectedLogo, error)
	AnalyzeCompany(ctx context.Context, companyName string) (*logodetection.CompanyAnalysis, error)
}

// Handler はロゴ検出・企業分析のHTTPリクエストを処理します。
type Handler struct {
	uc Usecase
}

// NewHandler はHandlerの新しいインスタンスを生成します。
func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// DetectLogos は画像をアップロードしてロゴを検出します。
//
// エンドポイント: POST /v1/logo/detect
// Content-Type: multipart/form-data
// フィールド: image（画像ファイル、最大10MB）
func (h *Handler) DetectLogos(w http.ResponseWriter, r *http.Request) {
	const maxImageSize = 10 * 1024 * 1024 // 10MB

	// multipart の境界・ヘッダ分の余裕を見込み、リクエスト全体のサイズを制限する。
	// 一時ファイルの肥大を防ぐため、ParseMultipartForm の前段でハードリミットをかける。
	r.Body = http.MaxBytesReader(w, r.Body, maxImageSize+1<<20)

	// ParseMultipartForm の引数はメモリ上限。
	if err := r.ParseMultipartForm(maxImageSize); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			slog.Warn("画像ファイルサイズ超過", "max", maxImageSize, "remote_addr", httpx.ClientIP(r))
			httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, api.ErrorResponse{Error: "image size exceeds the limit (10MB)"})
			return
		}
		slog.Warn("画像ファイルの取得に失敗", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "image file is required"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		slog.Warn("画像ファイルの取得に失敗", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "image file is required"})
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("画像ファイルのクローズに失敗", "error", err)
		}
	}()

	if header.Size > maxImageSize {
		slog.Warn("画像ファイルサイズ超過", "size", header.Size, "max", maxImageSize, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, api.ErrorResponse{Error: "image size exceeds the limit (10MB)"})
		return
	}

	imageData, err := io.ReadAll(io.LimitReader(file, maxImageSize+1))
	if err != nil {
		slog.Error("画像データの読み取りに失敗", "error", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "failed to read image"})
		return
	}

	logos, err := h.uc.DetectLogos(r.Context(), imageData)
	if err != nil {
		slog.Error("ロゴ検出に失敗", "error", err)
		httpx.WriteJSON(w, http.StatusBadGateway, api.ErrorResponse{Error: "logo detection failed"})
		return
	}

	out := make([]api.DetectedLogoResponse, 0, len(logos))
	for _, l := range logos {
		out = append(out, api.DetectedLogoResponse{
			Name:       l.Name,
			Confidence: l.Confidence,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// AnalyzeCompany は企業分析サマリーを生成します。
//
// エンドポイント: POST /v1/logo/analyze
// Content-Type: application/json
func (h *Handler) AnalyzeCompany(w http.ResponseWriter, r *http.Request) {
	var req api.CompanyAnalysisRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		slog.Warn("企業分析リクエストのデコードに失敗", "error", err, "remote_addr", httpx.ClientIP(r))
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid request"})
		return
	}

	analysis, err := h.uc.AnalyzeCompany(r.Context(), req.CompanyName)
	if err != nil {
		slog.Error("企業分析に失敗", "error", err, "company", req.CompanyName)
		httpx.WriteJSON(w, http.StatusBadGateway, api.ErrorResponse{Error: "company analysis failed"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, api.CompanyAnalysisResponse{
		CompanyName: analysis.CompanyName,
		Summary:     analysis.Summary,
	})
}
