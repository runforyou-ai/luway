//go:build server

package server

import (
	"context"
	"testing"
	"time"
	"uuid"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestQueueStatusesAndFailedRuns 验证队列概况只把已到执行时间的排队与重试任务计入等待，挂起与执行中任务分别计数，只统计近期失败，只有未到执行时间任务的队列不列出；失败列表列出近期失败与已出错的重试任务并按失败时间倒序。
func TestQueueStatusesAndFailedRuns(t *testing.T) {
	ctx, db, _ := newEnqueueTestRuntime(t)
	queue := "status-" + uuid.NewV7().String()
	scheduledQueue := "status-scheduled-" + uuid.NewV7().String()
	now := time.Now().UTC()
	// insertRun 写入一条指定状态的任务运行并在测试结束时删除。
	insertRun := func(queue, status string, availableAt time.Time, completedAt *time.Time, lastError *string, updatedAt time.Time) string {
		t.Helper()
		run := &servermodels.TaskRun{
			ActionName: "status.test", QueueName: queue, TriggerType: TriggerBusiness, Status: status, MaxAttempts: 3,
			AvailableAt: availableAt, CompletedAt: completedAt, LastError: lastError, UpdatedAt: updatedAt,
		}
		if _, err := db.NewInsert().Model(run).
			Column("action_name", "queue_name", "trigger_type", "status", "max_attempts", "available_at", "completed_at", "last_error", "updated_at").
			Returning("id").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", run.ID).Exec(context.Background())
		})
		return run.ID
	}
	insertRun(queue, statusQueued, now.Add(-10*time.Minute), nil, nil, now)
	insertRun(queue, statusPublished, now.Add(-time.Minute), nil, nil, now)
	insertRun(queue, statusQueued, now.Add(time.Hour), nil, nil, now)
	retrying := insertRun(queue, statusRetrying, now.Add(-2*time.Minute), nil, new("timeout"), now.Add(-time.Minute))
	insertRun(queue, statusRunning, now.Add(-time.Minute), nil, nil, now)
	insertRun(queue, statusPaused, now.Add(-time.Minute), nil, nil, now)
	failed := insertRun(queue, statusFailed, now.Add(-time.Hour), new(now.Add(-30*time.Minute)), new("boom"), now.Add(-30*time.Minute))
	insertRun(queue, statusFailed, now.Add(-30*24*time.Hour), new(now.Add(-RecentFailureWindow-time.Hour)), new("old"), now.Add(-RecentFailureWindow-time.Hour))
	insertRun(queue, statusSucceeded, now.Add(-time.Hour), new(now.Add(-time.Minute)), nil, now)

	insertRun(scheduledQueue, statusRetrying, now.Add(time.Hour), nil, new("later"), now)

	statuses, err := ReadQueueStatuses(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var status *QueueStatus
	for index := range statuses {
		if statuses[index].Queue == queue {
			status = &statuses[index]
		}
		if statuses[index].Queue == scheduledQueue {
			t.Fatalf("只有未到执行时间任务的队列被列出: %+v", statuses[index])
		}
	}
	if status == nil || status.Waiting != 3 || status.Running != 1 || status.Paused != 1 || status.Failed != 1 ||
		status.OldestWaitingSince == nil || status.OldestWaitingSince.Sub(now.Add(-10*time.Minute)).Abs() > time.Second {
		t.Fatalf("queue status = %+v", status)
	}

	runs, _, err := ListFailedRuns(ctx, db, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := make([]FailedRun, 0, 2)
	for _, run := range runs {
		if run.QueueName == queue {
			listed = append(listed, run)
		}
	}
	if len(listed) != 2 || listed[0].ID != retrying || !listed[0].Retrying || listed[0].LastError != "timeout" ||
		listed[1].ID != failed || listed[1].Retrying || listed[1].LastError != "boom" {
		t.Fatalf("failed runs = %+v", listed)
	}
}
