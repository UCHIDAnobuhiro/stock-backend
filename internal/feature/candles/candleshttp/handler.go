package candleshttp

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// symbolCodePattern は銘柄コードとして許可する形式（例: AAPL, 7203.T）。
// symbols.code が VARCHAR(20) のため最大20文字、英数字と . _ - のみ許可する。
var symbolCodePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,20}$`)

// Usecase はローソク足データ操作のユースケースインターフェースを定義します。
// Goの慣例に従い、インターフェースは利用者（handler）側で定義します。
type Usecase interface {
	GetCandles(ctx context.Context, symbol, interval string, outputsize int) ([]candles.Candle, error)
}

// Handler はローソク足データのHTTPリクエストを処理します。
type Handler struct {
	uc Usecase
}

// NewHandler は指定されたusecaseでHandlerの新しいインスタンスを生成します。
func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// GetCandlesHandler は銘柄コードと時間間隔を受け取り、ローソク足データをJSONで返します。
//
// エンドポイント例:
// GET /candles/{code}?interval=1day&outputsize=200
func (h *Handler) GetCandlesHandler(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if !symbolCodePattern.MatchString(code) {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid symbol code"})
		return
	}
	// 未指定の場合はデフォルト値を使用
	interval := queryOrDefault(r, "interval", "1day")
	// 空文字（?interval=）を含む未対応値は拒否する（OpenAPI spec の enum と整合）。
	if !candles.IsValidInterval(interval) {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "unsupported interval"})
		return
	}
	outputsizeStr := queryOrDefault(r, "outputsize", "200")
	// 文字列を整数に変換
	outputsize, err := strconv.Atoi(outputsizeStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "outputsize must be an integer"})
		return
	}

	candles, err := h.uc.GetCandles(r.Context(), code, interval, outputsize)
	if err != nil {
		slog.Error("failed to get candles", "error", err, "code", code)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	// データをフォーマット
	out := make([]api.CandleResponse, 0, len(candles))
	for _, x := range candles {
		out = append(out, api.CandleResponse{
			Time:   x.Time.UTC().Format("2006-01-02"),
			Open:   x.Open,
			High:   x.High,
			Low:    x.Low,
			Close:  x.Close,
			Volume: x.Volume,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

// queryOrDefault はクエリパラメータ key の値を返します。key が存在しない場合のみ def を返します。
// Gin の c.DefaultQuery と同じく、key が空文字で存在する場合（?interval=）は空文字を返します。
func queryOrDefault(r *http.Request, key, def string) string {
	q := r.URL.Query()
	if q.Has(key) {
		return q.Get(key)
	}
	return def
}
