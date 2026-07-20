package batch

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
)

const (
	rateLimitPerMinute = 7 // TwelveData APIのレートリミット（無料枠上限8/分、固定ウィンドウずれ対策で1つ余裕を持たせる）
)

// jobs は job_id とバッチ実行関数の対応表。
// 新しいバッチジョブを追加する場合はここに1行追加するだけでよい。
var jobs = map[string]func(*config.Config) int{
	"candles": runCandleIngest, // 株価取り込み
	"logo":    runLogoIngest,   // ロゴURL取り込み
}

// Run は job_id（コマンド引数）に応じてバッチを実行し、終了コードを返す。
// candles: 株価取り込み、logo: ロゴURL取り込み。
// 環境変数から読み込んだ設定は cfg として注入される。
// os.Exit は呼ばず、終了コードを返すのみ（呼び出し側の main で os.Exit する）。
func Run(cfg *config.Config, args []string) int {
	if len(args) < 1 {
		slog.Error("job_id is required", "usage", "batch <"+supportedJobs()+">")
		return 2
	}
	job, ok := jobs[args[0]]
	if !ok {
		slog.Error("unknown job_id", "job_id", args[0], "supported", supportedJobs())
		return 2
	}
	return job(cfg)
}

// supportedJobs は対応している job_id を辞書順で連結した文字列を返す（エラーメッセージ用）。
// map のイテレーション順は非決定的なので、ソートして出力を安定させる。
func supportedJobs() string {
	keys := make([]string, 0, len(jobs))
	for k := range jobs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
