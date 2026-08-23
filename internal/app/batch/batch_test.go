package batch

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestRun_DuplicateTriggersSkipJob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		jobID       string
		wantLockKey int32
	}{
		{name: "success: scheduler retry skips auth session cleanup", jobID: "auth-session-cleanup", wantLockKey: 1},
		{name: "success: manual overlap skips auth session cleanup", jobID: "auth-session-cleanup", wantLockKey: 1},
		{name: "success: scheduler retry skips candles", jobID: "candles", wantLockKey: 2},
		{name: "success: manual overlap skips candles", jobID: "candles", wantLockKey: 2},
		{name: "success: scheduler retry skips logo", jobID: "logo", wantLockKey: 3},
		{name: "success: manual overlap skips logo", jobID: "logo", wantLockKey: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			openCalls := 0
			jobCalls := 0
			availableJobs := map[string]job{
				tt.jobID: {
					lockKey: tt.wantLockKey,
					run: func(*config.Config, *sql.DB) int {
						jobCalls++
						return 0
					},
				},
			}

			got := run(
				&config.Config{},
				[]string{tt.jobID},
				availableJobs,
				func(infradb.Config) (*sql.DB, error) {
					openCalls++
					return newTestSQLDB(t), nil
				},
				func(_ context.Context, _ *sql.DB, namespace, key int32) (bool, func(context.Context) error, error) {
					assert.Equal(t, batchLockNamespace, namespace)
					assert.Equal(t, tt.wantLockKey, key)
					return false, nil, nil
				},
			)

			assert.Equal(t, 0, got)
			assert.Equal(t, 1, openCalls)
			assert.Equal(t, 0, jobCalls)
		})
	}
}

func TestRun_AcquiredLockRunsJobAndReleases(t *testing.T) {
	t.Parallel()

	openCalls := 0
	jobCalls := 0
	unlockCalls := 0
	var lockDB *sql.DB
	var jobDB *sql.DB
	availableJobs := map[string]job{
		"candles": {
			lockKey: 2,
			run: func(_ *config.Config, db *sql.DB) int {
				jobCalls++
				jobDB = db
				return 1
			},
		},
	}

	got := run(
		&config.Config{},
		[]string{"candles"},
		availableJobs,
		func(infradb.Config) (*sql.DB, error) {
			openCalls++
			return newTestSQLDB(t), nil
		},
		func(_ context.Context, db *sql.DB, _, _ int32) (bool, func(context.Context) error, error) {
			lockDB = db
			return true, func(context.Context) error {
				unlockCalls++
				return nil
			}, nil
		},
	)

	assert.Equal(t, 1, got)
	assert.Equal(t, 2, openCalls)
	assert.Equal(t, 1, jobCalls)
	assert.Equal(t, 1, unlockCalls)
	assert.NotSame(t, lockDB, jobDB)
}

func TestRun_UnlockErrorFailsAfterSuccessfulJob(t *testing.T) {
	t.Parallel()

	jobCalls := 0
	unlockCalls := 0
	availableJobs := map[string]job{
		"candles": {
			lockKey: 2,
			run: func(*config.Config, *sql.DB) int {
				jobCalls++
				return 0
			},
		},
	}

	got := run(
		&config.Config{},
		[]string{"candles"},
		availableJobs,
		func(infradb.Config) (*sql.DB, error) { return newTestSQLDB(t), nil },
		func(context.Context, *sql.DB, int32, int32) (bool, func(context.Context) error, error) {
			return true, func(context.Context) error {
				unlockCalls++
				return errors.New("unlock failed")
			}, nil
		},
	)

	assert.Equal(t, 1, got)
	assert.Equal(t, 1, jobCalls)
	assert.Equal(t, 1, unlockCalls)
}

func TestRun_LockErrorFailsWithoutRunningJob(t *testing.T) {
	t.Parallel()

	jobCalls := 0
	availableJobs := map[string]job{
		"logo": {
			lockKey: 3,
			run: func(*config.Config, *sql.DB) int {
				jobCalls++
				return 0
			},
		},
	}

	got := run(
		&config.Config{},
		[]string{"logo"},
		availableJobs,
		func(infradb.Config) (*sql.DB, error) { return newTestSQLDB(t), nil },
		func(context.Context, *sql.DB, int32, int32) (bool, func(context.Context) error, error) {
			return false, nil, errors.New("lock unavailable")
		},
	)

	assert.Equal(t, 1, got)
	assert.Equal(t, 0, jobCalls)
}

func TestRun_DuplicateDecisionIsLogged(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	availableJobs := map[string]job{
		"auth-session-cleanup": {
			lockKey: 1,
			run:     func(*config.Config, *sql.DB) int { return 0 },
		},
	}

	got := run(
		&config.Config{},
		[]string{"auth-session-cleanup"},
		availableJobs,
		func(infradb.Config) (*sql.DB, error) { return newTestSQLDB(t), nil },
		func(context.Context, *sql.DB, int32, int32) (bool, func(context.Context) error, error) {
			return false, nil, nil
		},
	)

	require.Equal(t, 0, got)
	logOutput := output.String()
	assert.True(t, strings.Contains(logOutput, "event=batch_skipped"), logOutput)
	assert.True(t, strings.Contains(logOutput, "job_id=auth-session-cleanup"), logOutput)
	assert.True(t, strings.Contains(logOutput, "reason=already_running"), logOutput)
}

func TestRun_LockErrorIsLogged(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	availableJobs := map[string]job{
		"logo": {
			lockKey: 3,
			run:     func(*config.Config, *sql.DB) int { return 0 },
		},
	}

	got := run(
		&config.Config{},
		[]string{"logo"},
		availableJobs,
		func(infradb.Config) (*sql.DB, error) { return newTestSQLDB(t), nil },
		func(context.Context, *sql.DB, int32, int32) (bool, func(context.Context) error, error) {
			return false, nil, errors.New("lock query failed")
		},
	)

	require.Equal(t, 1, got)
	logOutput := output.String()
	assert.True(t, strings.Contains(logOutput, "event=batch_lock_failed"), logOutput)
	assert.True(t, strings.Contains(logOutput, "job_id=logo"), logOutput)
	assert.True(t, strings.Contains(logOutput, `error="lock query failed"`), logOutput)
}

func newTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("pgx", "postgres://test:test@localhost:1/test?sslmode=disable")
	require.NoError(t, err)
	return db
}
