package logging

import (
	"bytes"
	"fmt"
	"log/slog"
	"runtime/debug"
)

// StackTraceKey は Cloud Error Reporting が検出するスタックトレースのログ属性キーです。
const StackTraceKey = "stack_trace"

// PanicStackAttr は Go の panic 出力形式のスタックトレース属性を返します。
// recover() を呼んだ defer 関数内から呼び出してください。
func PanicStackAttr(rec any) slog.Attr {
	return slog.String(StackTraceKey, "panic: "+fmt.Sprint(rec)+"\n\n"+trimPanicFrames(debug.Stack()))
}

// trimPanicFrames は先頭フレームが panic 発生地点になるよう、回復処理側のフレームを除去します。
// Error Reporting が別原因の panic を同一グループに集約しないための処理です。
func trimPanicFrames(stack []byte) string {
	lines := bytes.SplitAfter(stack, []byte("\n"))
	if len(lines) == 0 {
		return string(stack)
	}

	for i := 1; i < len(lines); i++ {
		if !bytes.HasPrefix(lines[i], []byte("panic(")) {
			continue
		}
		end := i + 1
		if end < len(lines) && bytes.HasPrefix(lines[end], []byte("\t")) {
			end++
		}
		out := append([][]byte{lines[0]}, lines[end:]...)
		return string(bytes.Join(out, nil))
	}
	return string(stack)
}
