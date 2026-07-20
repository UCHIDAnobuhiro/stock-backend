package candles

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	ingestOutputSize = 5000 // 1リクエストあたりの取得データポイント数（TwelveData 最大値）
)

// WriteRepository はローソク足データの書き込みレイヤーを抽象化します。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type WriteRepository interface {
	// UpsertBatch は（symbol, interval, time）をユニークキーとしてUpsert操作を行います。
	UpsertBatch(ctx context.Context, candles []Candle) error
}

// MarketRepository は株式市場データ取得のリポジトリインターフェースを定義します。
// 外部API実装を抽象化します。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type MarketRepository interface {
	// GetTimeSeries は loc を解釈ロケールとして、外部APIのタイムスタンプ文字列を
	// 取引所ローカル時刻として時系列データを返します。
	GetTimeSeries(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]Candle, error)
}

// ActiveSymbol は ingest 対象銘柄のコードとタイムゾーン情報を保持します。
// Timezone は IANA タイムゾーン文字列（例: "America/New_York", "Asia/Tokyo"）。
type ActiveSymbol struct {
	Code     string
	Timezone string
}

// SymbolRepository はデータ取り込み対象の銘柄取得を抽象化します。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type SymbolRepository interface {
	ListActiveSymbols(ctx context.Context) ([]ActiveSymbol, error)
}

// RateLimiter は外部 API 呼び出しの待機を抽象化します。
// Goの慣例に従い、インターフェースは利用者（usecase）側で定義します。
type RateLimiter interface {
	WaitIfNeeded(ctx context.Context) error
}

// IngestResult は IngestAll 実行後の銘柄単位の集計結果を表します。
// 致命的エラー時も部分集計が返されるため、main 側でサマリログを出力できます。
// 個別エラーの内容は IngestAll 内で slog.Error として出力されるため、
// 集約せず件数のみ保持します。
type IngestResult struct {
	Total     int // 取り込み対象銘柄数
	Succeeded int // 成功数
	Failed    int // 失敗数
}

// FailureRate は失敗率を [0.0, 1.0] で返します。Total が 0 の場合は 0 を返します。
func (r IngestResult) FailureRate() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Failed) / float64(r.Total)
}

// IngestUsecase は外部APIからデータを取得し、データベースに永続化するユースケースを定義します。
type IngestUsecase struct {
	market      MarketRepository
	candle      WriteRepository
	symbol      SymbolRepository
	rateLimiter RateLimiter
}

// NewIngestUsecase はIngestUsecaseの新しいインスタンスを生成します。
func NewIngestUsecase(market MarketRepository, candle WriteRepository, symbol SymbolRepository, rateLimiter RateLimiter) *IngestUsecase {
	return &IngestUsecase{market: market, candle: candle, symbol: symbol, rateLimiter: rateLimiter}
}

// IngestAll はアクティブな全銘柄の時系列データを取得し、
// 日足・週足・月足をデータベースに永続化します。
// APIレート制限を遵守し、必要に応じてリクエスト間で待機します。
//
// 銘柄単位の失敗は IngestResult に集約され処理は継続します。
// 致命的エラー（symbol 一覧取得失敗、ctx キャンセル、rateLimiter 失敗）は
// それまでの部分集計と共に error を返します。
func (iu *IngestUsecase) IngestAll(ctx context.Context) (IngestResult, error) {
	symbols, err := iu.symbol.ListActiveSymbols(ctx)
	if err != nil {
		return IngestResult{}, err
	}

	result := IngestResult{Total: len(symbols)}
	for _, s := range symbols {
		// WaitIfNeeded は limit 未到達なら cancelled ctx でも nil を返すため、
		// ループごとに明示的に ctx をチェックして早期離脱する。
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := iu.rateLimiter.WaitIfNeeded(ctx); err != nil {
			return result, err
		}
		if err := iu.ingestOne(ctx, s, ingestOutputSize); err != nil {
			// 1銘柄のエラーで処理を停止せず、エラーをログに記録して続行
			slog.Error("failed to ingest data", "symbol", s.Code, "error", err)
			result.Failed++
			continue
		}
		result.Succeeded++
	}
	return result, nil
}

// ingestOne は指定された銘柄の日足データを外部リポジトリから取得し、
// 週足・月足を集計して3種まとめてデータベースにバッチ挿入（または更新）します。
// sym.Timezone は IANA タイムゾーン文字列で、外部 API レスポンスの解釈および
// 集計境界判定（週月の開始）に使用されます。
func (iu *IngestUsecase) ingestOne(ctx context.Context, sym ActiveSymbol, outputsize int) error {
	loc, err := time.LoadLocation(sym.Timezone)
	if err != nil {
		return fmt.Errorf("load timezone %q: %w", sym.Timezone, err)
	}

	daily, err := iu.market.GetTimeSeries(ctx, sym.Code, "1day", outputsize, loc)
	if err != nil {
		return err
	}

	for i := range daily {
		daily[i].SymbolCode = sym.Code
		daily[i].Interval = "1day"
	}

	weekly := trimIncompleteFirstBucket(aggregateWeekly(daily, loc), daily, func(t time.Time) bool {
		return int(t.In(loc).Weekday()) == 1 // 月曜日が ISO 週の開始
	})
	for i := range weekly {
		weekly[i].SymbolCode = sym.Code
		weekly[i].Interval = "1week"
	}

	monthly := trimIncompleteFirstBucket(aggregateMonthly(daily, loc), daily, func(t time.Time) bool {
		return t.In(loc).Day() == 1 // 1日が月の開始
	})
	for i := range monthly {
		monthly[i].SymbolCode = sym.Code
		monthly[i].Interval = "1month"
	}

	all := make([]Candle, 0, len(daily)+len(weekly)+len(monthly))
	all = append(all, daily...)
	all = append(all, weekly...)
	all = append(all, monthly...)

	return iu.candle.UpsertBatch(ctx, dedupCandles(all))
}

// dedupCandles は (symbol, interval, time) の組み合わせが重複するエントリを除去します。
// TwelveData API が重複タイムスタンプを返した場合に ON CONFLICT DO UPDATE が
// 同一バッチ内で同じ行を2回更新しようとする PostgreSQL エラー (SQLSTATE 21000) を防ぎます。
func dedupCandles(candles []Candle) []Candle {
	seen := make(map[string]struct{}, len(candles))
	out := make([]Candle, 0, len(candles))
	for _, c := range candles {
		key := fmt.Sprintf("%s|%s|%d", c.SymbolCode, c.Interval, c.Time.Unix())
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, c)
		}
	}
	return out
}
