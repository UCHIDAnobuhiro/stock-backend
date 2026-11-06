package logging

import (
	"io"
	"log/slog"
)

// NewHandler はログ出力フォーマットを切り替えて slog ハンドラーを生成します。
//   - useJSON == true: Cloud Logging 対応の JSON ハンドラー（本番 / Cloud Run 向け）。ERROR 以上は Error Reporting 対象
//   - useJSON == false: 人が読みやすいプレーンな TextHandler（ローカル開発向け）
//
// Text 形式は Cloud Logging 連携を前提としないため、severity / message への
// リマップは行わず slog 既定のキー（level / msg）のまま出力します。
func NewHandler(w io.Writer, level slog.Level, useJSON bool) slog.Handler {
	if useJSON {
		return NewErrorReportingHandler(NewCloudLoggingHandler(w, level))
	}
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
}

// NewCloudLoggingHandler は Cloud Logging（Cloud Run）が構造化ログとして解釈できる
// JSON ハンドラーを生成します。slog 既定のキー（level / msg）を Cloud Logging の
// 特別フィールド（severity / message）にリマップします。time キーは既定のまま
// （Cloud Logging が timestamp として解釈可能）です。
//
// これにより Cloud Logging 上で severity フィルタ・色分けが効き、メッセージ本文が
// 正しく表示されます。
func NewCloudLoggingHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: cloudLoggingReplaceAttr,
	})
}

// cloudLoggingReplaceAttr は slog の属性を Cloud Logging 仕様にリマップします。
// httpRequest のようなネストしたグループ内のフィールドは対象外とするため、
// トップレベル（len(groups) == 0）のキーのみを変換します。
func cloudLoggingReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		// Cloud Logging の正式な severity 名は WARNING。slog の "WARN" は認識
		// されないため明示的に置換する（INFO / ERROR / DEBUG はそのまま一致）。
		if level, ok := a.Value.Any().(slog.Level); ok && level == slog.LevelWarn {
			a.Value = slog.StringValue("WARNING")
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}
