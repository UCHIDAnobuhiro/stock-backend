package candles

import "time"

// Candle は特定の銘柄・時間間隔におけるOHLCV（始値、高値、安値、終値、出来高）ローソク足データを表します。
type Candle struct {
	SymbolCode string    // 銘柄コード（例: "AAPL", "7203.T"）
	Interval   string    // 時間間隔（例: "1day", "1week", "1month"）
	Time       time.Time // このローソク足期間の開始タイムスタンプ
	Timezone   string    // symbols.timezone の IANA 名。キャッシュ復元時にも使用する
	Open       float64   // 始値
	High       float64   // 期間中の高値
	Low        float64   // 期間中の安値
	Close      float64   // 終値
	Volume     int64     // 出来高
}
