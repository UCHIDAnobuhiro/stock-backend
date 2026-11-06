package logging

import (
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panicStackFixture は recover 内で取得した debug.Stack を模した固定入力です。
const panicStackFixture = "goroutine 36 [running]:\n" +
	"runtime/debug.Stack()\n" +
	"\t/go/src/runtime/debug/stack.go:26 +0x64\n" +
	"example.com/logging.PanicStackAttr({0x1, 0x2})\n" +
	"\t/app/stack.go:21 +0x54\n" +
	"example.com/middleware.Recover.func1.1()\n" +
	"\t/app/recover.go:28 +0x138\n" +
	"panic({0x10?, 0x20?})\n" +
	"\t/go/src/runtime/panic.go:859 +0x120\n" +
	"example.com/app.(*usecase).Get(...)\n" +
	"\t/app/usecase.go:15\n" +
	"net/http.HandlerFunc.ServeHTTP(...)\n" +
	"\t/go/src/net/http/server.go:2200\n"

// TestTrimPanicFrames は panic( 行までの除去とフォールバックを検証します。
func TestTrimPanicFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "success: panic frames are removed after header",
			input: panicStackFixture,
			want: "goroutine 36 [running]:\n" +
				"example.com/app.(*usecase).Get(...)\n" +
				"\t/app/usecase.go:15\n" +
				"net/http.HandlerFunc.ServeHTTP(...)\n" +
				"\t/go/src/net/http/server.go:2200\n",
		},
		{
			name: "success: only the first panic( is removed",
			input: "goroutine 1 [running]:\n" +
				"panic({0x1?})\n\t/rt/panic.go:1\n" +
				"example.com/a.F()\n\t/a.go:1\n" +
				"panic({0x2?})\n\t/rt/panic.go:2\n",
			want: "goroutine 1 [running]:\n" +
				"example.com/a.F()\n\t/a.go:1\n" +
				"panic({0x2?})\n\t/rt/panic.go:2\n",
		},
		{
			name:  "success: panic( as last line without file line",
			input: "goroutine 1 [running]:\npanic({0x1?})",
			want:  "goroutine 1 [running]:\n",
		},
		{
			name:  "fallback: no panic( line returns input as is",
			input: "goroutine 1 [running]:\nfoo()\n\t/a.go:1\n",
			want:  "goroutine 1 [running]:\nfoo()\n\t/a.go:1\n",
		},
		{
			name:  "fallback: empty input",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, trimPanicFrames([]byte(tt.input)))
		})
	}
}

// TestPanicStackAttr は実際に panic を recover した場合の属性キー・先頭フレームを検証します。
func TestPanicStackAttr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rec     any
		wantMsg string
	}{
		{name: "success: string value", rec: "something went wrong", wantMsg: "something went wrong"},
		{name: "success: error value", rec: errors.New("boom"), wantMsg: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			attr := recoverStackAttr(tt.rec)

			require.Equal(t, "stack_trace", attr.Key)
			got := attr.Value.String()
			// ヘッダ直後の関数行が panic を起こした関数であること。
			head := "^panic: " + regexp.QuoteMeta(tt.wantMsg) + `\n\ngoroutine \d+ \[running\]:\n`
			assert.Regexp(t, head+`[^\n]*panicInNamedFunc`, got)
			assert.NotContains(t, got, "runtime/debug.Stack")
			assert.NotContains(t, got, "logging.PanicStackAttr(")
		})
	}
}

// recoverStackAttr は panicInNamedFunc を実行して recover し、defer 内で PanicStackAttr を呼びます。
func recoverStackAttr(rec any) (attr slog.Attr) {
	defer func() {
		if r := recover(); r != nil {
			attr = PanicStackAttr(r)
		}
	}()
	panicInNamedFunc(rec)
	return attr
}

// panicInNamedFunc はスタックの先頭フレームとして現れることを確認するための名前付き関数です。
func panicInNamedFunc(rec any) {
	panic(rec)
}
