package candles

import (
	"context"
	"time"
)

const (
	// MaxQuoteCodes は GetQuotes に一度に渡せる銘柄コードの最大件数です。
	MaxQuoteCodes = 50

	// MaxQuoteBars はスパークライン用に含められる終値の最大本数です。
	MaxQuoteBars = 500
)

// Quote は銘柄ごとの最新終値・前日比・スパークライン用終値配列を表します。
type Quote struct {
	Code          string    // 銘柄コード（例: "AAPL", "7203.T"）
	Time          time.Time // 最新足のタイムスタンプ
	Close         float64   // 最新終値
	PrevClose     float64   // 前日終値
	Change        float64   // 前日比（Close - PrevClose）
	ChangePercent float64   // 前日比率（%）。PrevCloseが0の場合は0
	Closes        []float64 // スパークライン用終値（古い→新しい順）。bars=0の場合はnil
}

// GetQuotes は指定された複数銘柄について、最新終値・前日比・スパークライン用の
// 終値配列を取得します。Repository.FindLatestBySymbols を1回呼び出し、全銘柄分の
// 直近データを unnest + CROSS JOIN LATERAL の一括SQLで取得します（quotes経路は
// Redisを経由しないため、CachingRepositoryはinnerへの単純委譲になります）。
//
// FindLatestBySymbols がエラーを返した場合は全体をエラーとして返します
// （部分成功にはしません）。ローソク足が2本未満の銘柄（前日比を計算できない）は
// 結果から除外します（エラーにはしません）。返却順序は codes の順に安定します。
func (cu *usecase) GetQuotes(ctx context.Context, codes []string, interval string, bars int) ([]Quote, error) {
	// 前日比の計算には最低2本のローソク足が必要なため、bars（スパークライン用件数）
	// が2未満でも常に2本以上を取得する。
	outputsize := max(bars, 2)

	flat, err := cu.candle.FindLatestBySymbols(ctx, codes, interval, outputsize)
	if err != nil {
		return nil, err
	}

	// フラットな結果を銘柄ごとにグルーピングする。FindLatestBySymbols は銘柄ごとに
	// 時刻降順で返すため、append 順のまま grouped[code] も時刻降順が保たれる。
	grouped := make(map[string][]Candle, len(codes))
	for _, c := range flat {
		grouped[c.SymbolCode] = append(grouped[c.SymbolCode], c)
	}

	// codes の順に buildQuote を呼び、2本未満（nil）の銘柄を除外する。
	quotes := make([]Quote, 0, len(codes))
	for _, code := range codes {
		if q := buildQuote(code, grouped[code], bars); q != nil {
			quotes = append(quotes, *q)
		}
	}

	return quotes, nil
}

// buildQuote は指定銘柄のローソク足（時刻降順、cs[0]が最新）から Quote を組み立てます。
// cs が2本未満の場合は前日比を計算できないため nil を返します
// （呼び出し元 GetQuotes が結果から除外します）。
func buildQuote(code string, cs []Candle, bars int) *Quote {
	if len(cs) < 2 {
		return nil
	}

	latest := cs[0]
	prevClose := cs[1].Close
	change := latest.Close - prevClose
	changePercent := 0.0
	if prevClose != 0 {
		changePercent = change / prevClose * 100
	}

	q := &Quote{
		Code:          code,
		Time:          latest.Time,
		Close:         latest.Close,
		PrevClose:     prevClose,
		Change:        change,
		ChangePercent: changePercent,
	}

	if bars > 0 {
		// cs は新しい→古い順なので、直近 n 本を古い→新しい順に反転して詰める。
		n := min(bars, len(cs))
		closes := make([]float64, n)
		for i := 0; i < n; i++ {
			closes[n-1-i] = cs[i].Close
		}
		q.Closes = closes
	}

	return q
}
