//go:build server

package server

import (
	"context"
	"fmt"
	"time"
)

const (
	// pruneRunsActionName 是清理终态任务运行的内置 Action。
	pruneRunsActionName = "task.runs.prune"
	// pruneRunsScheduleKey 是清理终态任务运行的定时计划标识。
	pruneRunsScheduleKey = "task-runs-prune"
	// terminalRunRetention 是终态任务运行的保留时长。
	terminalRunRetention = 7 * 24 * time.Hour
	// pruneRunsBatch 是单条删除语句处理的最大运行数。
	pruneRunsBatch = 5000
)

// registerPruneRuns 注册每小时清理超过保留期的终态任务运行的内置计划。
func (r *Runtime) registerPruneRuns() {
	if err := r.registry.RegisterJSON(pruneRunsActionName, func(ctx context.Context, _ struct{}) error {
		return r.repository.pruneTerminalRuns(ctx, time.Now().UTC().Add(-terminalRunRetention))
	}); err != nil {
		panic(err)
	}
	r.RegisterSchedule(ScheduleDefinition{
		Key: pruneRunsScheduleKey, ActionName: pruneRunsActionName, Queue: "maintenance", Payload: struct{}{},
		CronExpression: "@hourly", Timezone: "UTC", Enabled: true, MaxAttempts: 1, StartImmediately: true,
	})
}

// pruneTerminalRuns 分批删除完成时间早于 completedBefore 的成功和失败运行。
func (r *repository) pruneTerminalRuns(ctx context.Context, completedBefore time.Time) error {
	for {
		result, err := r.db.NewRaw(`
			DELETE FROM task_runs
			WHERE id IN (
				SELECT id FROM task_runs
				WHERE status IN (?, ?) AND completed_at < ?
				LIMIT ?
			)
		`, statusSucceeded, statusFailed, completedBefore, pruneRunsBatch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("prune terminal task runs: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read pruned task run count: %w", err)
		}
		if count < pruneRunsBatch {
			return nil
		}
	}
}
