package watchlisthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/watchlist"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/jwt"
)

// symbolCodePattern は銘柄コードとして許可する形式（例: AAPL, 7203.T）。
// symbols.code が VARCHAR(20) のため最大20文字、英数字と . _ - のみ許可する。
var symbolCodePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,20}$`)

// Usecase はウォッチリスト操作のユースケースインターフェースを定義します。
type Usecase interface {
	ListUserSymbols(ctx context.Context, userID int64) ([]watchlist.UserSymbol, error)
	AddSymbol(ctx context.Context, userID int64, symbolCode string) error
	RemoveSymbol(ctx context.Context, userID int64, symbolCode string) error
	ReorderSymbols(ctx context.Context, userID int64, orderedCodes []string) error
}

// Handler はウォッチリストに関連するHTTPリクエストを処理します。
type Handler struct {
	uc Usecase
}

// NewHandler はHandlerの新しいインスタンスを生成します。
func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// List はユーザーのウォッチリスト一覧を取得します。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	entries, err := h.uc.ListUserSymbols(r.Context(), userID)
	if err != nil {
		slog.Error("failed to list watchlist", "error", err, "userID", userID)
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	out := make([]api.WatchlistItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, api.WatchlistItem{
			Id:         e.ID,
			SymbolCode: e.SymbolCode,
			SortKey:    e.SortKey,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Add はウォッチリストに銘柄を追加します。
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	var req api.AddWatchlistRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid request"})
		return
	}
	if !symbolCodePattern.MatchString(req.SymbolCode) {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid symbol code"})
		return
	}

	if err := h.uc.AddSymbol(r.Context(), userID, req.SymbolCode); err != nil {
		switch {
		case errors.Is(err, watchlist.ErrSymbolNotFound):
			httpx.WriteJSON(w, http.StatusNotFound, api.ErrorResponse{Error: err.Error()})
		case errors.Is(err, watchlist.ErrAlreadyInWatchlist):
			httpx.WriteJSON(w, http.StatusConflict, api.ErrorResponse{Error: err.Error()})
		default:
			slog.Error("failed to add watchlist symbol", "error", err, "userID", userID)
			httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		}
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, api.MessageResponse{Message: "added to watchlist"})
}

// Remove はウォッチリストから銘柄を削除します。
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}
	code := chi.URLParam(r, "code")
	if !symbolCodePattern.MatchString(code) {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid symbol code"})
		return
	}

	if err := h.uc.RemoveSymbol(r.Context(), userID, code); err != nil {
		switch {
		case errors.Is(err, watchlist.ErrNotInWatchlist):
			httpx.WriteJSON(w, http.StatusNotFound, api.ErrorResponse{Error: err.Error()})
		default:
			slog.Error("failed to remove watchlist symbol", "error", err, "userID", userID)
			httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Reorder はウォッチリストの並び順を更新します。
func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	userID, ok := jwt.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		return
	}

	var req api.ReorderWatchlistRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid request"})
		return
	}
	for _, code := range req.Codes {
		if !symbolCodePattern.MatchString(code) {
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid symbol code"})
			return
		}
	}

	if err := h.uc.ReorderSymbols(r.Context(), userID, req.Codes); err != nil {
		switch {
		case errors.Is(err, watchlist.ErrReorderCodesMismatch):
			httpx.WriteJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: err.Error()})
		default:
			slog.Error("failed to reorder watchlist", "error", err, "userID", userID)
			httpx.WriteJSON(w, http.StatusInternalServerError, api.ErrorResponse{Error: "internal server error"})
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
