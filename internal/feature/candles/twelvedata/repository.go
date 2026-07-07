package twelvedata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
)

// TwelveDataMarket はTwelve Data外部APIから株価データを取得するMarketRepository実装です。
type TwelveDataMarket struct {
	cfg    Config
	client *http.Client
}

// TwelveDataMarketがMarketRepositoryを実装していることをコンパイル時に検証します。
var _ candles.MarketRepository = (*TwelveDataMarket)(nil)

// NewTwelveDataMarket は指定された設定とHTTPクライアントでTwelveDataMarketの新しいインスタンスを生成します。
func NewTwelveDataMarket(cfg Config, client *http.Client) *TwelveDataMarket {
	return &TwelveDataMarket{cfg: cfg, client: client}
}

// GetTimeSeries はTwelve Data APIから時系列株価データを取得し、
// domain.Candleのスライスとして返します。
// loc は外部 API レスポンスの datetime（取引所ローカル時刻）を解釈するロケーションです。
func (t *TwelveDataMarket) GetTimeSeries(ctx context.Context, symbol, interval string, outputsize int, loc *time.Location) ([]candles.Candle, error) {
	if loc == nil {
		return nil, fmt.Errorf("twelvedata: loc must not be nil")
	}
	q := url.Values{}
	// クエリパラメータを追加
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	q.Set("outputsize", strconv.Itoa(outputsize))

	// URLを生成
	u := fmt.Sprintf("%s/time_series?%s", t.cfg.BaseURL, q.Encode())

	res, err := t.doRequestWithRetry(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
		}
	}()

	// JSONレスポンスをDTOにデコード
	var body TimeSeriesResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status == "error" {
		return nil, fmt.Errorf("twelvedata: %s", body.Message)
	}

	result := make([]candles.Candle, 0, len(body.Values))
	for _, v := range body.Values {

		// タイムスタンプを取引所ローカル時刻として解釈
		tm, err := time.ParseInLocation("2006-01-02 15:04:05", v.Datetime, loc)
		if err != nil {
			tm, err = time.ParseInLocation("2006-01-02", v.Datetime, loc)
			if err != nil {
				return nil, fmt.Errorf("parse time %q: %w", v.Datetime, err)
			}
		}
		// 始値をパース
		o, err := strconv.ParseFloat(v.Open, 64)
		if err != nil {
			return nil, fmt.Errorf("parse open %q: %w", v.Open, err)
		}
		// 高値をパース
		h, err := strconv.ParseFloat(v.High, 64)
		if err != nil {
			return nil, fmt.Errorf("parse high %q: %w", v.High, err)
		}
		// 安値をパース
		l, err := strconv.ParseFloat(v.Low, 64)
		if err != nil {
			return nil, fmt.Errorf("parse low %q: %w", v.Low, err)
		}
		// 終値をパース
		c, err := strconv.ParseFloat(v.Close, 64)
		if err != nil {
			return nil, fmt.Errorf("parse close %q: %w", v.Close, err)
		}
		// 出来高をパース
		vol64, err := strconv.ParseInt(v.Volume, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse volume %q: %w", v.Volume, err)
		}

		// ドメインエンティティに変換
		result = append(result, candles.Candle{
			Time:   tm,
			Open:   o,
			High:   h,
			Low:    l,
			Close:  c,
			Volume: vol64,
		})
	}
	return result, nil
}

// doRequestWithRetry は指定された HTTP リクエストを実行し、
// ネットワークエラー・5xx・429 に対して指数バックオフ + ジッターでリトライします。
// 4xx（429 を除く）は即エラーを返し、ctx キャンセル時はリトライを中断します。
// 外側のレートリミッタとは独立に動作するため、リトライは外側のレート消費を増やしません。
func (t *TwelveDataMarket) doRequestWithRetry(ctx context.Context, method, urlStr string) (*http.Response, error) {
	maxAttempts := t.cfg.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// ctx が既にキャンセル済みなら即終了
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "apikey "+t.cfg.TwelveDataAPIKey)

		res, err := t.client.Do(req)
		if err != nil {
			// ctx 起因のエラーはリトライしない
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = err
			if attempt == maxAttempts-1 {
				break
			}
			if !t.sleepBeforeRetry(ctx, attempt, 0) {
				return nil, ctx.Err()
			}
			continue
		}

		// 成功
		if res.StatusCode < 400 {
			return res, nil
		}

		// リトライ対象外のエラーは即返す
		if !isRetryableStatus(res.StatusCode) {
			statusErr := fmt.Errorf("twelvedata http %d", res.StatusCode)
			_ = res.Body.Close()
			return nil, statusErr
		}

		// リトライ対象（5xx / 429）。最終試行ならエラーを返す。
		retryAfter := parseRetryAfter(res)
		lastErr = fmt.Errorf("twelvedata http %d", res.StatusCode)
		_ = res.Body.Close()

		if attempt == maxAttempts-1 {
			break
		}
		if !t.sleepBeforeRetry(ctx, attempt, retryAfter) {
			return nil, ctx.Err()
		}
	}

	if lastErr == nil {
		lastErr = errors.New("twelvedata: retry exhausted")
	}
	return nil, lastErr
}

// sleepBeforeRetry は次のリトライまで待機します。retryAfter > 0 ならそれを優先し、
// それ以外は attempt（0 起算）に応じた指数バックオフ + ジッターで待機します。
// ctx キャンセル時は false を返し即時に中断します。
func (t *TwelveDataMarket) sleepBeforeRetry(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	d := computeRetryDelay(attempt, retryAfter, t.cfg)
	if d <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// computeRetryDelay は次のリトライまでの待機時間を決定します。
// retryAfter > 0 ならそれを優先し、RetryMaxBackoff を上限にクランプします。
// retryAfter <= 0 の場合は attempt（0 起算）に応じた指数バックオフ + ジッターで決定します。
// 純粋関数として副作用なく実装され、テストから直接検証可能です。
func computeRetryDelay(attempt int, retryAfter time.Duration, cfg Config) time.Duration {
	d := retryAfter
	if d <= 0 {
		d = backoffDelay(attempt, cfg.RetryBaseBackoff, cfg.RetryMaxBackoff, cfg.RetryJitterRatio)
	} else if cfg.RetryMaxBackoff > 0 && d > cfg.RetryMaxBackoff {
		d = cfg.RetryMaxBackoff
	}
	if d < 0 {
		return 0
	}
	return d
}

// isRetryableStatus はリトライ対象の HTTP ステータスかを判定します。
// 5xx（500-599）と 429 が対象。401/403/404/422 等の 4xx はリトライ対象外。
func isRetryableStatus(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status < 600
}

// maxRetryAfterSecs は Retry-After で受け入れる秒数の上限（int64 オーバーフロー回避と現実的な上限のため 1 時間）。
const maxRetryAfterSecs = 3600

// parseRetryAfter は Retry-After ヘッダを time.Duration として解釈します。
// 数値（秒）と HTTP-date の両方をサポートし、解釈不能な場合は 0 を返します。
// 秒数は maxRetryAfterSecs（1 時間）でクランプし、time.Duration 変換時の int64 オーバーフローを防ぎます。
func parseRetryAfter(res *http.Response) time.Duration {
	v := res.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		if secs > maxRetryAfterSecs {
			secs = maxRetryAfterSecs
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		if d > maxRetryAfterSecs*time.Second {
			d = maxRetryAfterSecs * time.Second
		}
		return d
	}
	return 0
}

// backoffDelay は attempt（0 起算）に応じた指数バックオフ + ジッター付き待機時間を返します。
// base * 4^attempt（max でクリップ）に対し ±jitter の乱数を加算します。
func backoffDelay(attempt int, base, maxDelay time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		return 0
	}
	mult := math.Pow(4, float64(attempt))
	d := time.Duration(float64(base) * mult)
	if maxDelay > 0 && d > maxDelay {
		d = maxDelay
	}
	if jitter > 0 {
		// [-jitter, +jitter] の乱数比率を加算
		delta := (rand.Float64()*2 - 1) * jitter
		d = time.Duration(float64(d) * (1 + delta))
	}
	if d < 0 {
		return 0
	}
	return d
}
