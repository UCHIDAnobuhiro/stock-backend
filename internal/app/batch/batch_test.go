package batch

import (
	"testing"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/symbollist"
	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
)

// TestShouldFailExit はしきい値判定の境界条件を検証します。
// しきい値ちょうど（FailureRate == threshold）は許容し、超過時のみ true を返すこと。
// candles / logo 双方の result 型が failureRater を満たすことも兼ねて検証します。
func TestShouldFailExit(t *testing.T) {
	testCases := []struct {
		name      string
		result    failureRater
		threshold float64
		want      bool
	}{
		{
			name:      "candles: 全銘柄成功 → exit 0",
			result:    candles.IngestResult{Total: 10, Succeeded: 10, Failed: 0},
			threshold: 0.2,
			want:      false,
		},
		{
			name:      "candles: 失敗率がしきい値ちょうど → exit 0（許容）",
			result:    candles.IngestResult{Total: 10, Succeeded: 8, Failed: 2},
			threshold: 0.2,
			want:      false,
		},
		{
			name:      "candles: 失敗率がしきい値超過 → exit 1",
			result:    candles.IngestResult{Total: 10, Succeeded: 7, Failed: 3},
			threshold: 0.2,
			want:      true,
		},
		{
			name:      "candles: 全銘柄失敗 → exit 1",
			result:    candles.IngestResult{Total: 5, Succeeded: 0, Failed: 5},
			threshold: 0.2,
			want:      true,
		},
		{
			name:      "candles: Total=0（symbol 空） → exit 0",
			result:    candles.IngestResult{Total: 0},
			threshold: 0.2,
			want:      false,
		},
		{
			name:      "candles: threshold=0 で 1 件失敗 → exit 1（厳格モード）",
			result:    candles.IngestResult{Total: 10, Succeeded: 9, Failed: 1},
			threshold: 0,
			want:      true,
		},
		{
			name:      "candles: threshold=1.0 で全件失敗 → exit 0（最寛容）",
			result:    candles.IngestResult{Total: 5, Succeeded: 0, Failed: 5},
			threshold: 1.0,
			want:      false,
		},
		{
			name:      "logo: 全銘柄成功 → exit 0",
			result:    symbollist.LogoIngestResult{Total: 10, Succeeded: 10, Failed: 0},
			threshold: 0.2,
			want:      false,
		},
		{
			name:      "logo: 失敗率がしきい値ちょうど → exit 0（許容）",
			result:    symbollist.LogoIngestResult{Total: 10, Succeeded: 8, Failed: 2},
			threshold: 0.2,
			want:      false,
		},
		{
			name:      "logo: 失敗率がしきい値超過 → exit 1",
			result:    symbollist.LogoIngestResult{Total: 10, Succeeded: 7, Failed: 3},
			threshold: 0.2,
			want:      true,
		},
		{
			name:      "logo: Total=0（symbol 空） → exit 0",
			result:    symbollist.LogoIngestResult{Total: 0},
			threshold: 0.2,
			want:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldFailExit(tc.result, tc.threshold); got != tc.want {
				t.Errorf("shouldFailExit(%+v, %v)=%v, want %v", tc.result, tc.threshold, got, tc.want)
			}
		})
	}
}

// TestRunInvalidJobID は job_id 未指定・未知の値で exit code 2 を返すことを検証します。
// 各ジョブは DB 接続を伴うため、ここでは引数ディスパッチのエラー系のみを対象とします。
func TestRunInvalidJobID(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want int
	}{
		{name: "job_id 未指定", args: []string{}, want: 2},
		{name: "未知の job_id", args: []string{"bogus"}, want: 2},
	}

	cfg := &config.Config{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Run(cfg, tc.args); got != tc.want {
				t.Errorf("Run(%v)=%d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestRun_ReturnsOneWhenDBConfigInvalid(t *testing.T) {
	t.Parallel()

	// DB_USER 未設定相当の不正な DB Config → OpenSQL の検証で失敗し 1 を返す。
	cfg := &config.Config{DB: infradb.Config{}}
	for _, jobID := range []string{"auth-session-cleanup", "candles", "logo"} {
		t.Run(jobID, func(t *testing.T) {
			if got := Run(cfg, []string{jobID}); got != 1 {
				t.Errorf("Run(%q) = %d, want 1", jobID, got)
			}
		})
	}
}
