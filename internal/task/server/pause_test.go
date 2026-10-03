//go:build server

package server

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestSuspendedOrganizationPausesAndResumesRuns 验证所属工作区暂停时任务不被认领而是挂起，挂起任务仍占用幂等键，工作区恢复后重新排队并可认领。
func TestSuspendedOrganizationPausesAndResumesRuns(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	organization := &servermodels.Organization{
		Name: "暂停测试", Slug: "pause-" + strings.ReplaceAll(uuid.New().String(), "-", ""), LifecycleStatus: string(domain.OrganizationLifecycleSuspended),
	}
	if _, err := db.NewInsert().Model(organization).Column("name", "slug", "lifecycle_status").Returning("id").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.Organization)(nil)).Where("id = ?", organization.ID).Exec(context.Background())
	})
	options := EnqueueOptions{OrganizationID: organization.ID, IdempotencyKey: "pause:" + organization.ID}
	runID, err := runtime.Enqueue(ctx, actionName, struct{}{}, options)
	if err != nil {
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)

	if run, err := runtime.repository.claimRun(ctx, runID, "worker-1"); err != nil || run != nil {
		t.Fatalf("暂停工作区的任务被认领: run=%v err=%v", run, err)
	}
	if paused, err := runtime.repository.pauseRun(ctx, runID); err != nil || !paused {
		t.Fatalf("挂起任务 = %v, err = %v", paused, err)
	}
	if status := taskRunStatus(t, ctx, db, runID); status != statusPaused {
		t.Fatalf("任务状态 = %q，期望 %q", status, statusPaused)
	}
	if duplicateID, err := runtime.Enqueue(ctx, actionName, struct{}{}, options); err != nil || duplicateID != runID {
		t.Fatalf("挂起任务的幂等投递 = %q, err = %v，期望 %q", duplicateID, err, runID)
	}

	if _, err := db.NewUpdate().Model(organization).Set("lifecycle_status = ?", domain.OrganizationLifecycleActive).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var resumed int
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		resumed, err = ResumeOrganizationRunsIn(ctx, tx, organization.ID)
		return err
	}); err != nil || resumed != 1 {
		t.Fatalf("恢复任务数 = %d, err = %v", resumed, err)
	}
	if status := taskRunStatus(t, ctx, db, runID); status != statusQueued {
		t.Fatalf("恢复后任务状态 = %q，期望 %q", status, statusQueued)
	}
	if exists, err := taskOutboxExists(ctx, db, runID); err != nil || !exists {
		t.Fatalf("恢复后发件箱记录存在 = %v, err = %v", exists, err)
	}
	if run, err := runtime.repository.claimRun(ctx, runID, "worker-1"); err != nil || run == nil {
		t.Fatalf("恢复后任务未被认领: err=%v", err)
	}
}

// taskRunStatus 读取任务运行状态。
func taskRunStatus(t *testing.T, ctx context.Context, db *bun.DB, runID string) string {
	t.Helper()
	var status string
	if err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Column("status").Where("id = ?", runID).Scan(ctx, &status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestPauseWaitsForConcurrentResume 验证恢复工作区的事务提交前到达的挂起等待该事务，提交后读到正常状态而不挂起任务。
func TestPauseWaitsForConcurrentResume(t *testing.T) {
	ctx, db, runtime := newEnqueueTestRuntime(t)
	actionName := registerEnqueueTestAction(t, runtime)
	organization := &servermodels.Organization{
		Name: "并发恢复", Slug: "resume-" + strings.ReplaceAll(uuid.New().String(), "-", ""), LifecycleStatus: string(domain.OrganizationLifecycleSuspended),
	}
	if _, err := db.NewInsert().Model(organization).Column("name", "slug", "lifecycle_status").Returning("id").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.Organization)(nil)).Where("id = ?", organization.ID).Exec(context.Background())
	})
	runID, err := runtime.Enqueue(ctx, actionName, struct{}{}, EnqueueOptions{OrganizationID: organization.ID})
	if err != nil {
		t.Fatal(err)
	}
	cleanupEnqueuedTask(t, db, runID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.NewUpdate().Model(organization).Set("lifecycle_status = ?", domain.OrganizationLifecycleActive).WherePK().Exec(ctx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if resumed, err := ResumeOrganizationRunsIn(ctx, tx, organization.ID); err != nil || resumed != 0 {
		_ = tx.Rollback()
		t.Fatalf("恢复任务数 = %d, err = %v", resumed, err)
	}
	type pauseResult struct {
		paused bool
		err    error
	}
	done := make(chan pauseResult, 1)
	go func() {
		paused, err := runtime.repository.pauseRun(ctx, runID)
		done <- pauseResult{paused, err}
	}()
	select {
	case result := <-done:
		_ = tx.Rollback()
		t.Fatalf("恢复事务提交前挂起已返回: %+v", result)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.err != nil || result.paused {
		t.Fatalf("恢复提交后挂起 = %+v", result)
	}
	if status := taskRunStatus(t, ctx, db, runID); status != statusQueued {
		t.Fatalf("任务状态 = %q，期望 %q", status, statusQueued)
	}
}
