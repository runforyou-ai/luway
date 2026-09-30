//go:build server

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type executionContextKey struct{}

// Execution 标识当前任务的执行尝试，业务写回据此拒绝旧 Worker。
type Execution struct {
	TaskRunID  string
	Attempt    int
	workerID   *string
	finalizing bool
	exhausted  bool
}

var ErrExecutionLost = errors.New("task execution no longer owns the run")

// withExecution 把认领时的任务身份传入 Action 和终态回调。
func withExecution(ctx context.Context, run *servermodels.TaskRun, finalizing, exhausted bool) context.Context {
	return context.WithValue(ctx, executionContextKey{}, Execution{TaskRunID: run.ID, Attempt: run.Attempt, workerID: run.WorkerID, finalizing: finalizing, exhausted: exhausted})
}

// CurrentExecution 返回任务调用携带的当前尝试，直接 Action 调用不携带任务租约。
func CurrentExecution(ctx context.Context) (Execution, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}

// LockExecution 在业务事务末尾锁定并校验任务尝试，锁持续至业务提交。
func LockExecution(ctx context.Context, db bun.IDB) error {
	execution, ok := CurrentExecution(ctx)
	if !ok {
		return nil
	}
	query := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Column("id").
		Where("tr.id = ? AND tr.status = ? AND tr.attempt = ?", execution.TaskRunID, statusRunning, execution.Attempt).
		Where("tr.worker_id IS NOT DISTINCT FROM ?", execution.workerID)
	if execution.exhausted {
		query = query.Where("tr.attempt >= tr.max_attempts AND tr.lease_expires_at <= clock_timestamp()")
	} else if !execution.finalizing {
		query = query.Where("tr.lease_expires_at > clock_timestamp()")
	}
	var id string
	if err := query.For("UPDATE").Scan(ctx, &id); errors.Is(err, sql.ErrNoRows) {
		return ErrExecutionLost
	} else if err != nil {
		return fmt.Errorf("lock task execution: %w", err)
	}
	return nil
}
