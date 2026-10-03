//go:build server

package server

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestQueueStatusesAndFailedRuns 验证队列概况只把已到执行时间的排队任务计入等待，重试、挂起与执行中任务分别计数，只统计近期失败，只有未到执行时间排队任务的队列不列出；失败列表与概况范围一致，列出全部重试与近期失败并按失败时间倒序。
func TestQueueStatusesAndFailedRuns(t *testing.T) {
	ctx, db, _ := newEnqueueTestRuntime(t)
	queue := "status-" + uuid.NewV7().String()
	scheduledQueue := "status-scheduled-" + uuid.NewV7().String()
	now := time.Now().UTC()
	// insertRun 写入一条指定状态的任务运行并在测试结束时删除。
	insertRun := func(queue, status string, availableAt time.Time, failedAt *time.Time, lastError *string) string {
		t.Helper()
		run := &servermodels.TaskRun{
			ActionName: "status.test", QueueName: queue, TriggerType: TriggerBusiness, Status: status, MaxAttempts: 3,
			AvailableAt: availableAt, FailedAt: failedAt, LastError: lastError,
		}
		if _, err := db.NewInsert().Model(run).
			Column("action_name", "queue_name", "trigger_type", "status", "max_attempts", "available_at", "failed_at", "last_error").
			Returning("id").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", run.ID).Exec(context.Background())
		})
		return run.ID
	}
	insertRun(queue, statusQueued, now.Add(-10*time.Minute), nil, nil)
	insertRun(queue, statusPublished, now.Add(-time.Minute), nil, nil)
	insertRun(queue, statusQueued, now.Add(time.Hour), nil, nil)
	retrying := insertRun(queue, statusRetrying, now.Add(time.Hour), new(now.Add(-time.Minute)), new("timeout"))
	insertRun(queue, statusRunning, now.Add(-time.Minute), nil, nil)
	insertRun(queue, statusPaused, now.Add(-time.Minute), nil, nil)
	failed := insertRun(queue, statusFailed, now.Add(-time.Hour), new(now.Add(-30*time.Minute)), new("boom"))
	insertRun(queue, statusFailed, now.Add(-30*24*time.Hour), new(now.Add(-RecentFailureWindow-time.Hour)), new("old"))
	insertRun(queue, statusSucceeded, now.Add(-time.Hour), nil, nil)
	insertRun(scheduledQueue, statusQueued, now.Add(time.Hour), nil, nil)

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
			t.Fatalf("只有未到执行时间排队任务的队列被列出: %+v", statuses[index])
		}
	}
	if status == nil || status.Waiting != 2 || status.Running != 1 || status.Retrying != 1 || status.Paused != 1 || status.Failed != 1 ||
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

// TestFailedAtSurvivesRepublish 验证执行失败时记录失败时间，重试消息发布改写更新时间后失败列表仍按失败时间展示，执行成功后清空失败时间。
func TestFailedAtSurvivesRepublish(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	runID, err := runtime.Enqueue(ctx, actionName, struct{}{}, EnqueueOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)
	run, err := runtime.repository.claimRun(ctx, runID, "worker-1")
	if err != nil || run == nil {
		t.Fatalf("claim run = %v, err = %v", run, err)
	}
	if retry, err := runtime.repository.failRun(ctx, run, "worker-1", errors.New("upstream timeout"), false); err != nil || !retry {
		t.Fatalf("fail run retry = %v, err = %v", retry, err)
	}
	var recorded servermodels.TaskRun
	if err := db.NewSelect().Model(&recorded).Where("id = ?", runID).Scan(ctx); err != nil || recorded.FailedAt == nil {
		t.Fatalf("failed run = %+v, err = %v", recorded, err)
	}
	var messageID string
	if err := db.NewSelect().Table("task_outbox").Column("message_id").Where("task_run_id = ?", runID).Scan(ctx, &messageID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := runtime.repository.markPublished(ctx, runID, messageID); err != nil {
		t.Fatal(err)
	}
	runs, _, err := ListFailedRuns(ctx, db, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, listed := range runs {
		if listed.ID == runID {
			found = true
			if !listed.Retrying || !listed.FailedAt.Equal(*recorded.FailedAt) {
				t.Fatalf("listed run = %+v, recorded failed_at = %v", listed, recorded.FailedAt)
			}
		}
	}
	if !found {
		t.Fatal("等待重试的任务未出现在失败列表中")
	}

	// 退避时间到达后重新认领并执行成功。
	if _, err := db.NewUpdate().Model((*servermodels.TaskRun)(nil)).Set("available_at = ?", time.Now().UTC()).Where("id = ?", runID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	run, err = runtime.repository.claimRun(ctx, runID, "worker-2")
	if err != nil || run == nil {
		t.Fatalf("reclaim run = %v, err = %v", run, err)
	}
	if err := runtime.repository.completeRun(ctx, runID, "worker-2"); err != nil {
		t.Fatal(err)
	}
	var completed servermodels.TaskRun
	if err := db.NewSelect().Model(&completed).Where("id = ?", runID).Scan(ctx); err != nil || completed.FailedAt != nil || completed.LastError != nil {
		t.Fatalf("completed run = %+v, err = %v", completed, err)
	}
}
