package candles

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// MaxQuoteCodes は GetQuotes に一度に渡せる銘柄コードの最大件数です。
	MaxQuoteCodes = 50

	// MaxQuoteBars はスパークライン用に含められる終値の最大本数です。
	MaxQuoteBars = 500

	// quoteConcurrency は GetQuotes が銘柄ごとの Repository.Find を並行実行する際の
	// 最大同時実行数です。DB/Redis への同時アクセス数を抑えるため上限を設けます。
	quoteConcurrency = 8
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
// 終値配列を取得します。銘柄ごとに既存の Repository.Find（candles.CachingRepository
// 経由でワイヤリング済み）を呼び出すため、新しいSQL/sqlcクエリは追加しません。
//
// 1銘柄でも Repository.Find がエラーを返した場合は全体をエラーとして返します
// （部分成功にはしません）。ローソク足が2本未満の銘柄（前日比を計算できない）は
// 結果から除外します（エラーにはしません）。返却順序は保証しません。
func (cu *usecase) GetQuotes(ctx context.Context, codes []string, interval string, bars int) ([]Quote, error) {
	// 前日比の計算には最低2本のローソク足が必要なため、bars（スパークライン用件数）
	// が2未満でも常に2本以上を取得する。
	outputsize := max(bars, 2)

	// 銘柄ごとの結果を index 固定で書き込み、返却順序を安定させる
	// （順序保証は不要だが、goroutine 間の書き込み競合を避けるため）。
	results := make([]*Quote, len(codes))

	// DB/Redis への同時アクセス数を抑えるため、並行数を quoteConcurrency に制限する。
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(quoteConcurrency)

	for i, code := range codes {
		g.Go(func() error {
			cs, err := cu.candle.Find(gctx, code, interval, outputsize)
			if err != nil {
				return err
			}
			results[i] = buildQuote(code, cs, bars)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// 2本未満で buildQuote が nil を返した銘柄を除外する。
	quotes := make([]Quote, 0, len(results))
	for _, q := range results {
		if q != nil {
			quotes = append(quotes, *q)
		}
	}

	return quotes, nil
}

// buildQuote は Repository.Find が返したローソク足（時刻降順、cs[0]が最新）から
// Quote を組み立てます。cs が2本未満の場合は前日比を計算できないため nil を返します
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
