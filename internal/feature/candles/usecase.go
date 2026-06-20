package candles

import (
	"context"
)

const (
	// DefaultInterval はローソク足クエリのデフォルト時間間隔です。
	DefaultInterval = "1day"
	// DefaultOutputSize はデフォルトのローソク足返却件数です。
	DefaultOutputSize = 200
	// MaxOutputSize はローソク足の最大返却件数です。
	MaxOutputSize = 5000
)

// validIntervals は許可する時間間隔の集合です。
var validIntervals = map[string]struct{}{
	"1day":   {},
	"1week":  {},
	"1month": {},
}

// IsValidInterval は interval が許可された時間間隔かどうかを返します。
func IsValidInterval(interval string) bool {
	_, ok := validIntervals[interval]
	return ok
}

// Repository はローソク足データの読み取りレイヤーを抽象化します。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type Repository interface {
	// Find はデータベースからローソク足データを検索します。
	Find(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error)
}

// usecase はローソク足データ操作のユースケースを定義します。
type usecase struct {
	candle Repository
}

// NewUsecase はusecaseの新しいインスタンスを生成します。
func NewUsecase(candle Repository) *usecase {
	return &usecase{candle: candle}
}

// GetCandles は指定された銘柄と時間間隔のローソク足データを取得します。
func (cu *usecase) GetCandles(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
	if interval == "" {
		interval = DefaultInterval
	}
	if outputsize <= 0 || outputsize > MaxOutputSize {
		outputsize = DefaultOutputSize
	}

	cs, err := cu.candle.Find(ctx, symbol, interval, outputsize)
	if err != nil {
		return nil, err
	}

	return cs, nil
}
