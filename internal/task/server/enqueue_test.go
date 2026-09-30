//go:build server

package server

import (
	"context"
	"encoding/json"
	"testing"
	"uuid"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	"github.com/runforyou-ai/cervi/internal/servertest"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestEnqueueInCommitsWithBusinessTransaction 验证事务内任务只随调用方事务提交。
func TestEnqueueInCommitsWithBusinessTransaction(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)

	if exists, existsErr := taskRunExists(ctx, db, runID); existsErr != nil {
		_ = tx.Rollback()
		t.Fatal(existsErr)
	} else if exists {
		_ = tx.Rollback()
		t.Fatal("事务提交前任务运行记录对外可见")
	}
	if exists, existsErr := taskOutboxExists(ctx, db, runID); existsErr != nil {
		_ = tx.Rollback()
		t.Fatal(existsErr)
	} else if exists {
		_ = tx.Rollback()
		t.Fatal("事务提交前任务发件箱记录对外可见")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var run servermodels.TaskRun
	if err := db.NewSelect().Model(&run).Where("tr.id = ?", runID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.QueueName != defaultQueue || run.TriggerType != TriggerBusiness || run.MaxAttempts != defaultMaxAttempts {
		t.Fatalf("规范化任务参数 = queue:%q trigger:%q max_attempts:%d", run.QueueName, run.TriggerType, run.MaxAttempts)
	}
	var outbox servermodels.TaskOutbox
	if err := db.NewSelect().Model(&outbox).Where("tob.task_run_id = ?", runID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if outbox.QueueName != defaultQueue {
		t.Fatalf("发件箱队列 = %q，期望 %q", outbox.QueueName, defaultQueue)
	}
}

// TestEnqueueInRollsBackWithBusinessTransaction 验证调用方回滚时任务和发件箱同时回滚。
func TestEnqueueInRollsBackWithBusinessTransaction(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)
	if exists, existsErr := tx.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("tr.id = ?", runID).Exists(ctx); existsErr != nil {
		_ = tx.Rollback()
		t.Fatal(existsErr)
	} else if !exists {
		_ = tx.Rollback()
		t.Fatal("调用方事务内缺少任务运行记录")
	}
	if exists, existsErr := tx.NewSelect().Model((*servermodels.TaskOutbox)(nil)).Where("tob.task_run_id = ?", runID).Exists(ctx); existsErr != nil {
		_ = tx.Rollback()
		t.Fatal(existsErr)
	} else if !exists {
		_ = tx.Rollback()
		t.Fatal("调用方事务内缺少任务发件箱记录")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if exists, existsErr := taskRunExists(ctx, db, runID); existsErr != nil {
		t.Fatal(existsErr)
	} else if exists {
		t.Fatal("调用方回滚后仍存在任务运行记录")
	}
	if exists, existsErr := taskOutboxExists(ctx, db, runID); existsErr != nil {
		t.Fatal(existsErr)
	} else if exists {
		t.Fatal("调用方回滚后仍存在任务发件箱记录")
	}
}

// TestEnqueueInKeepsActiveIdempotency 验证事务内重复投递复用活动任务。
func TestEnqueueInKeepsActiveIdempotency(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	idempotencyKey := uuid.New().String()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstRunID, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{IdempotencyKey: idempotencyKey})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, firstRunID)
	secondRunID, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{IdempotencyKey: idempotencyKey})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if firstRunID != secondRunID {
		_ = tx.Rollback()
		t.Fatalf("重复事务投递返回任务 %q 和 %q", firstRunID, secondRunID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runCount, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).
		Where("tr.action_name = ?", actionName).
		Where("tr.idempotency_key = ?", idempotencyKey).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outboxCount, err := db.NewSelect().Model((*servermodels.TaskOutbox)(nil)).
		Where("tob.task_run_id = ?", firstRunID).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || outboxCount != 1 {
		t.Fatalf("幂等投递记录数 = task_runs:%d task_outbox:%d", runCount, outboxCount)
	}
}

// TestEnqueueManyInKeepsOrderAndIdempotency 验证批量投递按输入顺序返回运行编号，并复用批内与已有的活动任务。
func TestEnqueueManyInKeepsOrderAndIdempotency(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	cleanupEnqueuedActions(t, db, actionName)
	batchKey, existingKey := uuid.New().String(), uuid.New().String()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	existingRunID, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{IdempotencyKey: existingKey})
	if err != nil {
		t.Fatal(err)
	}
	runIDs, err := runtime.EnqueueManyIn(ctx, tx, []EnqueueRequest{
		{ActionName: actionName, Options: EnqueueOptions{IdempotencyKey: batchKey}},
		{ActionName: actionName},
		{ActionName: actionName, Options: EnqueueOptions{IdempotencyKey: batchKey}},
		{ActionName: actionName, Options: EnqueueOptions{IdempotencyKey: existingKey}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 4 || runIDs[0] == "" || runIDs[1] == "" || runIDs[0] == runIDs[1] || runIDs[2] != runIDs[0] || runIDs[3] != existingRunID {
		t.Fatalf("批量投递运行编号 = %v，已有任务 = %s", runIDs, existingRunID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runCount, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("tr.action_name = ?", actionName).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outboxCount, err := db.NewSelect().Model((*servermodels.TaskOutbox)(nil)).Where("tob.task_run_id IN (?)", bun.In([]string{existingRunID, runIDs[0], runIDs[1]})).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runCount != 3 || outboxCount != 3 {
		t.Fatalf("批量投递记录数 = task_runs:%d task_outbox:%d", runCount, outboxCount)
	}
}

// TestEnqueueInRejectsInvalidInputBeforeWriting 验证无效投递在写入前返回校验错误。
func TestEnqueueInRejectsInvalidInputBeforeWriting(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	unregisteredActionName := "test.unregistered." + uuid.New().String()
	cleanupEnqueuedActions(t, db, unregisteredActionName, actionName)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.EnqueueIn(ctx, tx, unregisteredActionName, struct{}{}, EnqueueOptions{}); err == nil {
		_ = tx.Rollback()
		t.Fatal("未注册 Action 投递未返回错误")
	}
	if _, err := runtime.EnqueueIn(ctx, tx, actionName, struct{}{}, EnqueueOptions{Queue: "invalid queue"}); err == nil {
		_ = tx.Rollback()
		t.Fatal("非法队列投递未返回错误")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).
		Where("tr.action_name IN (?, ?)", unregisteredActionName, actionName).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("无效投递写入了 %d 条任务运行记录", count)
	}
}

// newEnqueueTestRuntime 创建使用测试 PostgreSQL 的任务运行时。
func newEnqueueTestRuntime(t *testing.T) (context.Context, *bun.DB, *Runtime) {
	t.Helper()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	return ctx, db, New(db, serverconfig.NATSConfig{})
}

// registerEnqueueTestAction 注册空实现的测试 Action。
func registerEnqueueTestAction(t *testing.T, runtime *Runtime) string {
	t.Helper()
	actionName := "test.enqueue." + uuid.New().String()
	if err := runtime.Registry().Register(actionName, func(context.Context, json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return actionName
}

// cleanupEnqueuedTask 清理已经提交的测试任务。
func cleanupEnqueuedTask(t *testing.T, db *bun.DB, runID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id = ?", runID).Exec(ctx); err != nil {
			t.Errorf("清理测试任务发件箱失败: %v", err)
		}
		if _, err := db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", runID).Exec(ctx); err != nil {
			t.Errorf("清理测试任务运行失败: %v", err)
		}
	})
}

// cleanupEnqueuedActions 清理无效输入测试可能意外提交的任务。
func cleanupEnqueuedActions(t *testing.T, db *bun.DB, actionNames ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		var runIDs []string
		if err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).
			Column("id").
			Where("action_name IN (?)", bun.In(actionNames)).
			Scan(ctx, &runIDs); err != nil {
			t.Errorf("读取待清理测试任务失败: %v", err)
			return
		}
		if len(runIDs) == 0 {
			return
		}
		if _, err := db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id IN (?)", bun.In(runIDs)).Exec(ctx); err != nil {
			t.Errorf("清理测试任务发件箱失败: %v", err)
		}
		if _, err := db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id IN (?)", bun.In(runIDs)).Exec(ctx); err != nil {
			t.Errorf("清理测试任务运行失败: %v", err)
		}
	})
}

// taskRunExists 判断任务运行记录是否存在。
func taskRunExists(ctx context.Context, db bun.IDB, runID string) (bool, error) {
	return db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("tr.id = ?", runID).Exists(ctx)
}

// taskOutboxExists 判断任务发件箱记录是否存在。
func taskOutboxExists(ctx context.Context, db bun.IDB, runID string) (bool, error) {
	return db.NewSelect().Model((*servermodels.TaskOutbox)(nil)).Where("tob.task_run_id = ?", runID).Exists(ctx)
}
