package batch

import (
	"context"
	"database/sql"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/app/config"
	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
)

const (
	rateLimitPerMinute              = 7 // TwelveData APIのレートリミット（無料枠上限8/分、固定ウィンドウずれ対策で1つ余裕を持たせる）
	batchLockOperationTimeout       = 5 * time.Second
	batchLockNamespace        int32 = 0x53544f43 // "STOC"。他用途のadvisory lockとの衝突を避ける固定namespace。
)

type job struct {
	lockKey int32
	run     func(*config.Config, *sql.DB) int
}

type sqlOpener func(infradb.Config) (*sql.DB, error)

type jobLocker func(
	ctx context.Context,
	db *sql.DB,
	namespace int32,
	key int32,
) (acquired bool, unlock func(context.Context) error, err error)

// jobs は job_id とバッチ実行関数の対応表。
// 新しいバッチジョブを追加する場合はここに1行追加するだけでよい。
var jobs = map[string]job{
	"auth-session-cleanup": {lockKey: 1, run: runAuthSessionCleanup}, // 期限切れ認証セッション削除
	"candles":              {lockKey: 2, run: runCandleIngest},       // 株価取り込み
	"logo":                 {lockKey: 3, run: runLogoIngest},         // ロゴURL取り込み
}

// Run は job_id（コマンド引数）に応じてバッチを実行し、終了コードを返す。
// auth-session-cleanup: 期限切れ認証セッション削除、candles: 株価取り込み、logo: ロゴURL取り込み。
// 環境変数から読み込んだ設定は cfg として注入される。
// os.Exit は呼ばず、終了コードを返すのみ（呼び出し側の main で os.Exit する）。
func Run(cfg *config.Config, args []string) int {
	return run(cfg, args, jobs, infradb.OpenSQL, tryJobLock)
}

func run(
	cfg *config.Config,
	args []string,
	availableJobs map[string]job,
	openSQL sqlOpener,
	lockJob jobLocker,
) (exitCode int) {
	if len(args) < 1 {
		slog.Error("job_id is required", "usage", "batch <"+supportedJobs(availableJobs)+">")
		return 2
	}
	jobID := args[0]
	selectedJob, ok := availableJobs[jobID]
	if !ok {
		slog.Error("unknown job_id", "job_id", jobID, "supported", supportedJobs(availableJobs))
		return 2
	}

	lockDB, err := openSQL(cfg.DB)
	if err != nil {
		slog.Error("DB open failed", "job_id", jobID, "purpose", "batch_lock", "error", err)
		return 1
	}
	defer func() {
		if err := lockDB.Close(); err != nil {
			slog.Warn("failed to close sqlDB", "job_id", jobID, "purpose", "batch_lock", "error", err)
		}
	}()

	lockCtx, cancel := context.WithTimeout(context.Background(), batchLockOperationTimeout)
	acquired, unlock, err := lockJob(
		lockCtx,
		lockDB,
		batchLockNamespace,
		selectedJob.lockKey,
	)
	cancel()
	if err != nil {
		slog.Error("batch lock acquisition failed",
			"event", "batch_lock_failed",
			"job_id", jobID,
			"error", err,
		)
		return 1
	}
	if !acquired {
		slog.Info("batch skipped because the same job is already running",
			"event", "batch_skipped",
			"job_id", jobID,
			"reason", "already_running",
		)
		return 0
	}

	slog.Info("batch lock acquired", "event", "batch_lock_acquired", "job_id", jobID)
	defer func() {
		if err := releaseJobLock(jobID, unlock); err != nil && exitCode == 0 {
			exitCode = 1
		}
	}()

	sqlDB, err := openSQL(cfg.DB)
	if err != nil {
		slog.Error("DB open failed", "job_id", jobID, "purpose", "job", "error", err)
		return 1
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("failed to close sqlDB", "job_id", jobID, "purpose", "job", "error", err)
		}
	}()

	return selectedJob.run(cfg, sqlDB)
}

// supportedJobs は対応している job_id を辞書順で連結した文字列を返す（エラーメッセージ用）。
// map のイテレーション順は非決定的なので、ソートして出力を安定させる。
func supportedJobs(availableJobs map[string]job) string {
	return strings.Join(slices.Sorted(maps.Keys(availableJobs)), ", ")
}

func tryJobLock(
	ctx context.Context,
	db *sql.DB,
	namespace int32,
	key int32,
) (bool, func(context.Context) error, error) {
	lock, acquired, err := infradb.TryAdvisoryLock(ctx, db, namespace, key)
	if err != nil || !acquired {
		return acquired, nil, err
	}
	return true, lock.Unlock, nil
}

func releaseJobLock(jobID string, unlock func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), batchLockOperationTimeout)
	defer cancel()
	if err := unlock(ctx); err != nil {
		slog.Error("batch lock release failed",
			"event", "batch_lock_release_failed",
			"job_id", jobID,
			"error", err,
		)
		return err
	}
	slog.Info("batch lock released", "event", "batch_lock_released", "job_id", jobID)
	return nil
}
