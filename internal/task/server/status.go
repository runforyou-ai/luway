//go:build server

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/jetq"
)

// QueueStatus 定义一个队列的任务概况：Waiting 为尚未开始执行的任务数，Running 为执行中的任务数，Failed 为 FailureRetention 内最终失败的任务数。
type QueueStatus struct {
	Queue   string
	Waiting int
	Running int
	Failed  int
}

// TaskStatus 定义任务概况：Queues 为各队列的任务数，按队列名排列；Delayed 为推迟到指定时间执行的任务数，包括延迟投递、等待重试、工作区暂停或等待路由而推迟的任务。
type TaskStatus struct {
	Queues  []QueueStatus
	Delayed int
}

// FailedRun 定义一次最终失败的任务：WorkspaceID 为所属工作区，平台级任务为空，FailedAt 为最终失败的时间。
type FailedRun struct {
	ID          string
	WorkspaceID *string
	ActionName  string
	QueueName   string
	Attempt     int
	LastError   string
	FailedAt    time.Time
}

// TaskStatus 返回各队列的任务数与推迟执行的任务数。
func (r *Runtime) TaskStatus(ctx context.Context) (TaskStatus, error) {
	stats, err := r.client.Stats(ctx)
	if err != nil {
		return TaskStatus{}, fmt.Errorf("read task queue statuses: %w", err)
	}
	statuses := make([]QueueStatus, 0, len(stats.Queues))
	for _, queue := range stats.Queues {
		statuses = append(statuses, QueueStatus{
			Queue: queue.Queue, Waiting: int(queue.Ready), Running: queue.InFlight, Failed: int(queue.Dead),
		})
	}
	return TaskStatus{Queues: statuses, Delayed: int(stats.Delayed)}, nil
}

// FailedRuns 按失败时间倒序返回 FailureRetention 内最终失败的任务中的一页及总数，limit 为 0 时返回全部。
func (r *Runtime) FailedRuns(ctx context.Context, limit, offset int) ([]FailedRun, int, error) {
	stats, err := r.client.Stats(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count failed tasks: %w", err)
	}
	total := 0
	for _, queue := range stats.Queues {
		total += int(queue.Dead)
	}
	want := total
	if limit > 0 {
		want = min(total, offset+limit)
	}
	runs := make([]FailedRun, 0)
	if want <= offset {
		return runs, total, nil
	}
	letters, err := r.client.DeadLetters(ctx, jetq.DeadLetterQuery{Limit: want})
	if err != nil {
		return nil, 0, fmt.Errorf("list failed tasks: %w", err)
	}
	for _, letter := range letters[min(offset, len(letters)):] {
		run := FailedRun{ID: letter.ID, ActionName: letter.Name, QueueName: letter.Queue, Attempt: letter.Attempts, LastError: letter.Error, FailedAt: letter.FailedAt}
		if workspaceID := letter.Header.Get(headerWorkspace); workspaceID != "" {
			run.WorkspaceID = &workspaceID
		}
		runs = append(runs, run)
	}
	return runs, total, nil
}
