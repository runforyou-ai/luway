//go:build server

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// RecentFailureWindow 是运行概况统计失败任务的时间范围，与终态任务运行的保留时长一致。
const RecentFailureWindow = terminalRunRetention

// QueueStatus 定义一个队列的任务运行概况：Waiting 为已到执行时间仍在排队的任务数，OldestWaitingSince 为其中最早的到期时间，没有排队任务时为空；
// Running 为执行中的任务数，Retrying 为执行失败后等待重试的任务数，Paused 为所属工作区暂停而挂起的任务数，Failed 为 RecentFailureWindow 内失败且不再重试的任务数。
type QueueStatus struct {
	Queue              string     `bun:"queue_name"`
	Waiting            int        `bun:"waiting"`
	OldestWaitingSince *time.Time `bun:"oldest_waiting_since"`
	Running            int        `bun:"running"`
	Retrying           int        `bun:"retrying"`
	Paused             int        `bun:"paused"`
	Failed             int        `bun:"failed"`
}

// FailedRun 定义一次失败或等待重试的任务运行；OrganizationID 为所属工作区，平台级任务为空，FailedAt 为最近一次执行失败的时间。
type FailedRun struct {
	ID             string    `bun:"id"`
	OrganizationID *string   `bun:"organization_id"`
	ActionName     string    `bun:"action_name"`
	QueueName      string    `bun:"queue_name"`
	Retrying       bool      `bun:"retrying"`
	Attempt        int       `bun:"attempt"`
	MaxAttempts    int       `bun:"max_attempts"`
	LastError      string    `bun:"last_error"`
	FailedAt       time.Time `bun:"failed_at"`
}

// ReadQueueStatuses 返回至少有一项计数不为零的各队列运行概况，按队列名排列；只有未到执行时间排队任务的队列不列出。
func ReadQueueStatuses(ctx context.Context, db bun.IDB) ([]QueueStatus, error) {
	now := time.Now().UTC()
	failedSince := now.Add(-RecentFailureWindow)
	statuses := make([]QueueStatus, 0)
	if err := db.NewRaw(`
		SELECT queue_name,
			count(*) FILTER (WHERE status IN (?, ?) AND available_at <= ?) AS waiting,
			min(available_at) FILTER (WHERE status IN (?, ?) AND available_at <= ?) AS oldest_waiting_since,
			count(*) FILTER (WHERE status = ?) AS running,
			count(*) FILTER (WHERE status = ?) AS retrying,
			count(*) FILTER (WHERE status = ?) AS paused,
			count(*) FILTER (WHERE status = ?) AS failed
		FROM task_runs
		WHERE (status IN (?, ?) AND available_at <= ?) OR status IN (?, ?, ?) OR (status = ? AND failed_at >= ?)
		GROUP BY queue_name
		ORDER BY queue_name
	`, statusQueued, statusPublished, now,
		statusQueued, statusPublished, now,
		statusRunning, statusRetrying, statusPaused, statusFailed,
		statusQueued, statusPublished, now, statusRunning, statusRetrying, statusPaused, statusFailed, failedSince).
		Scan(ctx, &statuses); err != nil {
		return nil, fmt.Errorf("read task queue statuses: %w", err)
	}
	return statuses, nil
}

// ListFailedRuns 按最近一次失败时间倒序返回全部等待重试与 RecentFailureWindow 内失败的任务运行中的一页及总数，与 ReadQueueStatuses 的重试和失败计数范围一致。
func ListFailedRuns(ctx context.Context, db bun.IDB, limit, offset int) ([]FailedRun, int, error) {
	runs := make([]FailedRun, 0)
	total, err := db.NewSelect().TableExpr("task_runs").
		ColumnExpr("id::text AS id, organization_id::text AS organization_id, action_name, queue_name, status = ? AS retrying", statusRetrying).
		ColumnExpr("attempt, max_attempts, coalesce(last_error, '') AS last_error, failed_at").
		Where("status = ? OR (status = ? AND failed_at >= ?)", statusRetrying, statusFailed, time.Now().UTC().Add(-RecentFailureWindow)).
		OrderExpr("failed_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		ScanAndCount(ctx, &runs)
	if err != nil {
		return nil, 0, fmt.Errorf("list failed task runs: %w", err)
	}
	return runs, total, nil
}
