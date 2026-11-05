package main

import (
	"log/slog"
	"os"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/server"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/logging"
)

// main は設定を読み込んでロガーを設定し、server.Run の戻り値で os.Exit するだけの薄いラッパー。
// os.Exit は defer を実行しないため、後処理が走るよう実体は internal/app/server に分離している。
func main() {
	// 環境変数を一括で読み込み・検証する（os.Getenv の呼び出しは config に集約）。
	cfg, err := config.LoadAPI()
	// ロガーは設定読み込みの成否に関わらず構成する（cfg.Log は best-effort で埋まる）。
	logger := slog.New(logging.NewHandler(os.Stdout, cfg.Log.Level, cfg.Log.UseJSON))
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings {
		slog.Warn(w)
	}
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	os.Exit(server.Run(cfg))
}
