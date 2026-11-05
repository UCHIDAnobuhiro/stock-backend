package symbollisthttp

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// Usecase は銘柄（株式コード）操作のユースケースインターフェースを定義します。
// Goの慣例に従い、インターフェースは利用者（handler）側で定義します。
type Usecase interface {
	ListActiveSymbols(ctx context.Context) ([]symbollist.Symbol, error)
}

// Handler は銘柄情報に関連するHTTPリクエストを処理します。
type Handler struct {
	uc Usecase
}

// NewHandler はHandlerの新しいインスタンスを生成します。
func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// List はアクティブな銘柄の一覧を取得します。
// ユースケースを呼び出して銘柄リストを取得し、DTOに変換してJSONレスポンスとして返します。
// ユースケースがエラーを返した場合は500 Internal Server Errorを返します。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	symbols, err := h.uc.ListActiveSymbols(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to list symbols", "error", err)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}
	out := make([]api.SymbolItem, 0, len(symbols))
	for _, s := range symbols {
		out = append(out, api.SymbolItem{Code: s.Code, Name: s.Name, LogoUrl: s.LogoURL})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
