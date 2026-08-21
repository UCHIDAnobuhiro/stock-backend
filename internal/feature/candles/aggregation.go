package candles

import (
	"fmt"
	"slices"
	"time"
)

// aggregateWeekly はISO週ごとに日足ローソク足を集計して週足を生成します。
// 入力は任意の順序でよく、出力は時刻昇順で返されます。
// loc は週境界の判定および代表タイムスタンプ生成に使う取引所ローカルのロケーションです。
func aggregateWeekly(daily []Candle, loc *time.Location) []Candle {
	return aggregate(daily, weekKeyFn(loc), weekStartFn(loc))
}

// aggregateMonthly は月ごとに日足ローソク足を集計して月足を生成します。
// 入力は任意の順序でよく、出力は時刻昇順で返されます。
// loc は月境界の判定および代表タイムスタンプ生成に使う取引所ローカルのロケーションです。
func aggregateMonthly(daily []Candle, loc *time.Location) []Candle {
	return aggregate(daily, monthKeyFn(loc), monthStartFn(loc))
}

// trimIncompleteFirstBucket は最古の日足がバケット開始日でない場合、最初の集計バケットを除外します。
// 取得データの先頭が週・月の途中から始まる場合に、不完全なバケットで既存の完全なレコードを
// 上書きすることを防ぎます。isBucketStart は与えられた時刻がバケット（週・月）の開始日かどうかを返します。
func trimIncompleteFirstBucket(result []Candle, daily []Candle, isBucketStart func(time.Time) bool) []Candle {
	if len(result) == 0 || len(daily) == 0 {
		return result
	}
	oldest := daily[0].Time
	for _, c := range daily[1:] {
		if c.Time.Before(oldest) {
			oldest = c.Time
		}
	}
	if !isBucketStart(oldest) {
		return result[1:]
	}
	return result
}

// aggregate は日足スライスを keyFn で定義したバケットに集計します。
// startFn はバケットの代表タイムスタンプ（週月の開始日）を返します。
func aggregate(
	daily []Candle,
	keyFn func(time.Time) string,
	startFn func(time.Time) time.Time,
) []Candle {
	if len(daily) == 0 {
		return nil
	}

	// APIは最新順で返すため時刻昇順にソート（Open=初日, Close=末日 を正しく取るため）
	sorted := slices.Clone(daily)
	slices.SortFunc(sorted, func(a, b Candle) int {
		return a.Time.Compare(b.Time)
	})

	type bucket struct {
		open   float64
		high   float64
		low    float64
		close  float64
		volume int64
		time   time.Time
	}

	buckets := map[string]*bucket{}
	keyOrder := []string{} // 出現順（= 時刻順）を保持

	for _, c := range sorted {
		k := keyFn(c.Time)
		b, exists := buckets[k]
		if !exists {
			b = &bucket{
				open:   c.Open,
				high:   c.High,
				low:    c.Low,
				close:  c.Close,
				volume: c.Volume,
				time:   startFn(c.Time),
			}
			buckets[k] = b
			keyOrder = append(keyOrder, k)
		} else {
			if c.High > b.high {
				b.high = c.High
			}
			if c.Low < b.low {
				b.low = c.Low
			}
			b.close = c.Close // 昇順ソート済みなので最後の値が終値
			b.volume += c.Volume
		}
	}

	out := make([]Candle, 0, len(keyOrder))
	for _, k := range keyOrder {
		b := buckets[k]
		out = append(out, Candle{
			// SymbolCode と Interval は呼び出し元（ingestOne）でセットする
			Time:   b.time,
			Open:   b.open,
			High:   b.high,
			Low:    b.low,
			Close:  b.close,
			Volume: b.volume,
		})
	}
	return out
}

// weekKeyFn は loc ロケーションでの ISO 週番号に基づくバケットキー関数を返します（例: "2023-W01"）。
func weekKeyFn(loc *time.Location) func(time.Time) string {
	return func(t time.Time) string {
		year, week := t.In(loc).ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	}
}

// weekStartFn は loc ロケーションでその日が属する ISO 週の月曜日 00:00:00 を返す関数を返します。
func weekStartFn(loc *time.Location) func(time.Time) time.Time {
	return func(t time.Time) time.Time {
		local := t.In(loc)
		wd := int(local.Weekday())
		if wd == 0 {
			wd = 7 // 日曜日を ISO 準拠で 7 に補正
		}
		monday := local.AddDate(0, 0, -(wd - 1))
		return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, loc)
	}
}

// monthKeyFn は loc ロケーションでの年月に基づくバケットキー関数を返します（例: "2023-01"）。
func monthKeyFn(loc *time.Location) func(time.Time) string {
	return func(t time.Time) string {
		local := t.In(loc)
		return fmt.Sprintf("%04d-%02d", local.Year(), int(local.Month()))
	}
}

// monthStartFn は loc ロケーションでその日が属する月の 1 日 00:00:00 を返す関数を返します。
func monthStartFn(loc *time.Location) func(time.Time) time.Time {
	return func(t time.Time) time.Time {
		local := t.In(loc)
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	}
}
