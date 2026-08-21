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

// QuoteFailureReason は銘柄ごとの株価サマリー取得失敗理由を表します。
type QuoteFailureReason string

const (
	// QuoteFailureFetchFailed はリポジトリからローソク足を取得できなかったことを表します。
	QuoteFailureFetchFailed QuoteFailureReason = "fetch_failed"

	// QuoteFailureInsufficientData は前日比の計算に必要なローソク足が2本未満であることを表します。
	QuoteFailureInsufficientData QuoteFailureReason = "insufficient_data"
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

// QuoteFailure は銘柄ごとの株価サマリー取得失敗を表します。
// Cause はログ記録用の内部エラーであり、HTTPレスポンスには公開しません。
type QuoteFailure struct {
	Code   string
	Reason QuoteFailureReason
	Cause  error
}

// QuoteBatchResult は複数銘柄の株価サマリー取得結果を表します。
// 重複除去後の各入力銘柄は Quotes または Failures のどちらか一方に含まれます。
type QuoteBatchResult struct {
	Quotes   []Quote
	Failures []QuoteFailure
}

type quoteAttempt struct {
	quote   *Quote
	failure *QuoteFailure
}

// GetQuotes は指定された複数銘柄について、最新終値・前日比・スパークライン用の
// 終値配列を取得します。銘柄ごとに既存の Repository.Find（candles.CachingRepository
// 経由でワイヤリング済み）を呼び出すため、新しいSQL/sqlcクエリは追加しません。
//
// Repository.Find のエラーとローソク足が2本未満の銘柄は Failures に格納し、
// 他銘柄の取得は継続します。リクエストコンテキストが中断された場合だけ全体エラーを返します。
// Quotes と Failures はそれぞれ入力順を維持します。
func (cu *usecase) GetQuotes(ctx context.Context, codes []string, interval string, bars int) (QuoteBatchResult, error) {
	// 前日比の計算には最低2本のローソク足が必要なため、bars（スパークライン用件数）
	// が2未満でも常に2本以上を取得する。
	outputsize := max(bars, 2)

	// 銘柄ごとの結果を index 固定で書き込み、入力順を維持しつつ
	// goroutine 間の書き込み競合を避ける。
	attempts := make([]quoteAttempt, len(codes))

	// DB/Redis への同時アクセス数を抑えるため、並行数を quoteConcurrency に制限する。
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(quoteConcurrency)

	for i, code := range codes {
		g.Go(func() error {
			cs, err := cu.candle.Find(gctx, code, interval, outputsize)
			if err != nil {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				attempts[i].failure = &QuoteFailure{
					Code:   code,
					Reason: QuoteFailureFetchFailed,
					Cause:  err,
				}
				return nil
			}

			quote := buildQuote(code, cs, bars)
			if quote == nil {
				attempts[i].failure = &QuoteFailure{
					Code:   code,
					Reason: QuoteFailureInsufficientData,
				}
				return nil
			}

			attempts[i].quote = quote
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return QuoteBatchResult{}, err
	}

	result := QuoteBatchResult{
		Quotes:   make([]Quote, 0, len(attempts)),
		Failures: make([]QuoteFailure, 0, len(attempts)),
	}
	for _, attempt := range attempts {
		if attempt.quote != nil {
			result.Quotes = append(result.Quotes, *attempt.quote)
		}
		if attempt.failure != nil {
			result.Failures = append(result.Failures, *attempt.failure)
		}
	}

	return result, nil
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
		for i := range n {
			closes[n-1-i] = cs[i].Close
		}
		q.Closes = closes
	}

	return q
}
