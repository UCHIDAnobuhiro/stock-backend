package logodetection

// CompanyAnalysis は企業の分析結果を表します。
type CompanyAnalysis struct {
	CompanyName string  // 分析対象の企業名
	Ticker      *string // ティッカーシンボル。不明・非上場の場合はnil
	Summary     string  // AI生成の企業分析サマリー
}
