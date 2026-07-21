package candleshttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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
	GetQuotes(ctx context.Context, codes []string, interval string, bars int) ([]candles.Quote, error)
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
	// 範囲外の outputsize はキャッシュ有無で挙動が分かれてしまう（cache-hit は全件返し、
	// DB直読みは500になる）ため、ここで一律に拒否しリポジトリ層まで到達させない。
	if outputsize < 1 || outputsize > candles.MaxOutputSize {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: candles.ErrInvalidOutputSize.Error()})
		return
	}

	result, err := h.uc.GetCandles(r.Context(), code, interval, outputsize)
	if err != nil {
		// リポジトリ/キャッシュ層がラップして ErrInvalidOutputSize を返す可能性があるため、
		// 事前バリデーション（上記 outputsize チェック）と挙動を揃える防御的な分岐。
		if errors.Is(err, candles.ErrInvalidOutputSize) {
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: candles.ErrInvalidOutputSize.Error()})
			return
		}
		slog.Error("failed to get candles", "error", err, "code", code)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	// データをフォーマット
	out := make([]api.CandleResponse, 0, len(result))
	for _, x := range result {
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

// GetQuotesHandler は複数銘柄コードを受け取り、銘柄ごとの最新終値・前日比・
// スパークライン用終値配列をJSONで返します。
//
// エンドポイント例:
// GET /quotes?codes=AAPL,GOOGL,7203.T&interval=1day&bars=60
func (h *Handler) GetQuotesHandler(w http.ResponseWriter, r *http.Request) {
	codesStr := r.URL.Query().Get("codes")
	if codesStr == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "codes is required"})
		return
	}
	rawCodes := strings.Split(codesStr, ",")
	if len(rawCodes) > candles.MaxQuoteCodes {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "too many codes"})
		return
	}
	for _, code := range rawCodes {
		if !symbolCodePattern.MatchString(code) {
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid symbol code"})
			return
		}
	}
	// 重複コードは順序を保って除去してからusecaseに渡す。
	codes := dedupCodes(rawCodes)

	// 未指定の場合はデフォルト値を使用
	interval := queryOrDefault(r, "interval", "1day")
	// 空文字（?interval=）を含む未対応値は拒否する（OpenAPI spec の enum と整合）。
	if !candles.IsValidInterval(interval) {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "unsupported interval"})
		return
	}

	barsStr := queryOrDefault(r, "bars", "0")
	bars, err := strconv.Atoi(barsStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "bars must be an integer"})
		return
	}
	if bars < 0 || bars > candles.MaxQuoteBars {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "bars out of range"})
		return
	}

	result, err := h.uc.GetQuotes(r.Context(), codes, interval, bars)
	if err != nil {
		// 1銘柄でもリポジトリ層がエラーを返した場合、部分成功にはせず全体を500にする。
		slog.Error("failed to get quotes", "error", err, "codes", codes)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	// データをフォーマット
	out := make([]api.QuoteResponse, 0, len(result))
	for _, q := range result {
		item := api.QuoteResponse{
			Code:          q.Code,
			Time:          q.Time.UTC().Format("2006-01-02"),
			Close:         q.Close,
			PrevClose:     q.PrevClose,
			Change:        q.Change,
			ChangePercent: q.ChangePercent,
		}
		// closes は bars > 0 のときのみレスポンスに含める。
		if bars > 0 {
			closes := q.Closes
			item.Closes = &closes
		}
		out = append(out, item)
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

// dedupCodes は入力順序を保ったまま重複コードを除去します。
func dedupCodes(codes []string) []string {
	seen := make(map[string]struct{}, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// queryOrDefault はクエリパラメータ key の値を返します。key が存在しない場合のみ def を返します。
// key が空文字で存在する場合（?interval=）は空文字を返します。
func queryOrDefault(r *http.Request, key, def string) string {
	q := r.URL.Query()
	if q.Has(key) {
		return q.Get(key)
	}
	return def
}
