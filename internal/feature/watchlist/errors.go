package watchlist

import "errors"

var (
	// ErrSymbolNotFound は指定された銘柄コードが symbols テーブルに存在しない場合のエラーです。
	ErrSymbolNotFound = errors.New("symbol not found")

	// ErrAlreadyInWatchlist は銘柄が既にウォッチリストに存在する場合のエラーです。
	ErrAlreadyInWatchlist = errors.New("symbol already in watchlist")

	// ErrNotInWatchlist は削除対象の銘柄がウォッチリストに存在しない場合のエラーです。
	ErrNotInWatchlist = errors.New("symbol not in watchlist")

	// ErrReorderCodesMismatch は並び替えの codes がユーザーの watchlist 全件と
	// 一致しない（過不足・重複あり）場合のエラーです。
	ErrReorderCodesMismatch = errors.New("reorder codes do not match watchlist")
)
