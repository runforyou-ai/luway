//go:build server

package integrationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// runtimeProbeInput 是运行时测试任务的输入。
type runtimeProbeInput struct {
	Name string `json:"name"`
}

// runtimeProbe 记录运行时测试任务的执行与最终失败收尾。
type runtimeProbe struct {
	mu         sync.Mutex
	runs       map[string][]servertask.Execution
	finalized  map[string]error
	finalizing map[string]servertask.Execution
}

// record 记录一次任务执行及其携带的执行尝试。
func (p *runtimeProbe) record(ctx context.Context, name string) {
	execution, _ := servertask.CurrentExecution(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runs[name] = append(p.runs[name], execution)
}

// executions 返回指定任务已执行的尝试。
func (p *runtimeProbe) executions(name string) []servertask.Execution {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]servertask.Execution(nil), p.runs[name]...)
}

// finalizedWith 返回指定任务最终失败收尾收到的执行尝试与错误信息，未收尾时 ok 为 false。
func (p *runtimeProbe) finalizedWith(name string) (servertask.Execution, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	runErr, ok := p.finalized[name]
	if !ok {
		return servertask.Execution{}, "", false
	}
	return p.finalizing[name], runErr.Error(), true
}

// TestTaskRuntime 以进程内 NATS 验证服务端任务运行时：事务内投递只在提交后执行、回滚后丢弃；所属工作区未启用时推迟到启用后执行；
// 带路由的任务由持有路由租约的实例执行；永久失败与重试耗尽后执行最终失败收尾，并在队列概况与失败任务中报告。
func TestTaskRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	db := store.DB()
	workspaceID := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "任务运行时", DisplayName: "管理员", Email: servertest.UniqueEmail("task-runtime"), Password: "password123"}).Identity.Workspace.ID
	paused := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "暂停的工作区", DisplayName: "管理员", Email: servertest.UniqueEmail("task-runtime-paused"), Password: "password123"}).Identity.Workspace.ID
	_, err = db.NewUpdate().Table("workspaces").Set("lifecycle_status = ?", domain.WorkspaceLifecycleSuspended).Where("id = ?", paused).Exec(ctx)
	require.NoError(t, err)

	nats := servertest.StartRealtimeBroker(t)
	suffix := make([]byte, 4)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	instanceID := uuid.NewV7().String()
	runtime, err := servertask.New(ctx, nats.JS, servertask.Options{Namespace: "t" + hex.EncodeToString(suffix), Replicas: 1}, db, db, instanceID)
	require.NoError(t, err)
	probe := &runtimeProbe{runs: map[string][]servertask.Execution{}, finalized: map[string]error{}, finalizing: map[string]servertask.Execution{}}
	require.NoError(t, runtime.Registry().RegisterJSON("test.runtime.probe", func(ctx context.Context, input runtimeProbeInput) error {
		probe.record(ctx, input.Name)
		return nil
	}))
	// 首次尝试按指定延迟重试，之后的尝试一律失败；名称为 permanent 时首次尝试即永久失败。
	require.NoError(t, runtime.Registry().RegisterJSONWithTerminalFailure("test.runtime.failing", func(ctx context.Context, input runtimeProbeInput) error {
		probe.record(ctx, input.Name)
		if input.Name == "permanent" {
			return servertask.Permanent(errors.New("permanent failure"))
		}
		if len(probe.executions(input.Name)) == 1 {
			return servertask.RetryAfter(100*time.Millisecond, errors.New("temporary failure"))
		}
		return errors.New("still failing")
	}, func(ctx context.Context, input runtimeProbeInput, runErr error) error {
		execution, _ := servertask.CurrentExecution(ctx)
		probe.mu.Lock()
		defer probe.mu.Unlock()
		probe.finalized[input.Name], probe.finalizing[input.Name] = runErr, execution
		return nil
	}))
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(runtime.Stop)
	// waitRuns 等待指定任务执行到 count 次。
	waitRuns := func(name string, count int) {
		t.Helper()
		require.Eventually(t, func() bool { return len(probe.executions(name)) >= count }, 10*time.Second, 20*time.Millisecond, "task %s runs", name)
	}

	// 事务内投递在提交前不执行，提交后执行一次并携带本实例的执行尝试。
	var committedID string
	require.NoError(t, serverstorage.RunInTx(ctx, db, func(ctx context.Context, _ bun.Tx) error {
		id, err := runtime.EnqueueIn(ctx, "test.runtime.probe", runtimeProbeInput{Name: "committed"}, servertask.EnqueueOptions{WorkspaceID: workspaceID})
		committedID = id
		if err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
		require.Empty(t, probe.executions("committed"), "task ran before commit")
		return nil
	}))
	waitRuns("committed", 1)
	require.Equal(t, []servertask.Execution{{TaskRunID: committedID, Attempt: 1, InstanceID: instanceID}}, probe.executions("committed"))

	// 回滚的事务不投递任务。
	rollback := errors.New("rollback")
	err = serverstorage.RunInTx(ctx, db, func(ctx context.Context, _ bun.Tx) error {
		if _, err := runtime.EnqueueIn(ctx, "test.runtime.probe", runtimeProbeInput{Name: "rolled-back"}, servertask.EnqueueOptions{WorkspaceID: workspaceID}); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	// 同一去重键的任务结束前不重复投递，结束后可再次投递。
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "keyed"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, IdempotencyKey: "keyed", Delay: time.Hour}))
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "keyed-duplicate"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, IdempotencyKey: "keyed"}))
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "keyed-once"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, IdempotencyKey: "keyed-once"}))
	waitRuns("keyed-once", 1)
	// 去重键在任务确认完成时释放，重复投递直到再次执行。
	require.Eventually(t, func() bool {
		require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "keyed-once"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, IdempotencyKey: "keyed-once"}))
		return len(probe.executions("keyed-once")) >= 2
	}, 10*time.Second, 200*time.Millisecond, "re-enqueue after completion")
	require.Empty(t, probe.executions("keyed-duplicate"), "duplicate ran while the keyed task was pending")

	// 未登记的 Action 拒绝投递。
	require.Error(t, runtime.Enqueue(ctx, "test.runtime.unknown", runtimeProbeInput{}, servertask.EnqueueOptions{}))

	// 带路由的任务由持有路由租约的本实例执行。
	held, err := runtime.HoldRoute(ctx, "test-route:"+instanceID, time.Minute)
	require.NoError(t, err)
	require.True(t, held)
	t.Cleanup(func() { _ = runtime.ReleaseRoute(context.Background(), "test-route:"+instanceID) })
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "routed"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, Route: "test-route:" + instanceID}))
	waitRuns("routed", 1)

	// 所属工作区未启用时任务推迟执行，启用后执行。
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.probe", runtimeProbeInput{Name: "paused"}, servertask.EnqueueOptions{WorkspaceID: paused}))
	time.Sleep(2 * time.Second)
	require.Empty(t, probe.executions("paused"), "task ran while workspace paused")
	_, err = db.NewUpdate().Table("workspaces").Set("lifecycle_status = ?", domain.WorkspaceLifecycleActive).Where("id = ?", paused).Exec(ctx)
	require.NoError(t, err)

	// 永久失败不重试，直接执行最终失败收尾。
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.failing", runtimeProbeInput{Name: "permanent"}, servertask.EnqueueOptions{WorkspaceID: workspaceID, MaxAttempts: 3}))
	// 重试耗尽最大尝试次数后执行最终失败收尾。
	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.failing", runtimeProbeInput{Name: "exhausted"}, servertask.EnqueueOptions{MaxAttempts: 2}))
	require.Eventually(t, func() bool {
		_, _, permanent := probe.finalizedWith("permanent")
		_, _, exhausted := probe.finalizedWith("exhausted")
		return permanent && exhausted
	}, 10*time.Second, 20*time.Millisecond, "terminal failure")
	require.Len(t, probe.executions("permanent"), 1, "permanent failure retried")
	permanentExecution, permanentErr, _ := probe.finalizedWith("permanent")
	require.Contains(t, permanentErr, "permanent failure")
	require.True(t, permanentExecution.Finalizing, "finalize execution")
	require.Len(t, probe.executions("exhausted"), 2, "exhausted attempts")
	exhaustedExecution, exhaustedErr, _ := probe.finalizedWith("exhausted")
	require.Contains(t, exhaustedErr, "still failing")
	require.Equal(t, 2, exhaustedExecution.Attempt)

	// 最终失败的任务计入队列概况并按失败时间倒序列出。
	require.Eventually(t, func() bool {
		status, err := runtime.TaskStatus(ctx)
		if !assert.NoError(t, err) {
			return false
		}
		for _, status := range status.Queues {
			if status.Queue == servertask.QueueDefault {
				return status.Failed == 2
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "failed queue status")
	failed, total, err := runtime.FailedRuns(ctx, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, failed, 2)
	byName := map[string]servertask.FailedRun{}
	for _, run := range failed {
		require.Equal(t, "test.runtime.failing", run.ActionName)
		require.Equal(t, servertask.QueueDefault, run.QueueName)
		require.False(t, run.FailedAt.IsZero())
		if run.WorkspaceID != nil {
			byName["permanent"] = run
		} else {
			byName["exhausted"] = run
		}
	}
	require.Equal(t, workspaceID, *byName["permanent"].WorkspaceID)
	require.Equal(t, 1, byName["permanent"].Attempt)
	require.Contains(t, byName["permanent"].LastError, "permanent failure")
	require.Equal(t, 2, byName["exhausted"].Attempt)
	require.Contains(t, byName["exhausted"].LastError, "still failing")
	page, total, err := runtime.FailedRuns(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, page, 1)
	require.Equal(t, failed[1].ID, page[0].ID)

	// 启用后的工作区在推迟时长结束后执行原任务；回滚事务中的任务始终未执行。
	require.Eventually(t, func() bool { return len(probe.executions("paused")) == 1 }, 45*time.Second, 100*time.Millisecond, "paused task after activation")
	require.Empty(t, probe.executions("rolled-back"), "rolled back task ran")
}

// TestTaskRuntimeRemovedWorkspace 验证所属工作区已删除时任务不执行，直接最终失败并执行收尾，不再按暂停推迟。
func TestTaskRuntimeRemovedWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	db := store.DB()
	removed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "已删除的工作区", DisplayName: "管理员", Email: servertest.UniqueEmail("task-runtime-removed"), Password: "password123"}).Identity.Workspace.ID
	_, err = db.NewUpdate().Table("workspaces").Set("lifecycle_status = ?", domain.WorkspaceLifecycleDeleted).Where("id = ?", removed).Exec(ctx)
	require.NoError(t, err)

	nats := servertest.StartRealtimeBroker(t)
	suffix := make([]byte, 4)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	runtime, err := servertask.New(ctx, nats.JS, servertask.Options{Namespace: "t" + hex.EncodeToString(suffix), Replicas: 1}, db, db, uuid.NewV7().String())
	require.NoError(t, err)
	probe := &runtimeProbe{runs: map[string][]servertask.Execution{}, finalized: map[string]error{}, finalizing: map[string]servertask.Execution{}}
	require.NoError(t, runtime.Registry().RegisterJSONWithTerminalFailure("test.runtime.removed", func(ctx context.Context, input runtimeProbeInput) error {
		probe.record(ctx, input.Name)
		return nil
	}, func(ctx context.Context, input runtimeProbeInput, runErr error) error {
		execution, _ := servertask.CurrentExecution(ctx)
		probe.mu.Lock()
		defer probe.mu.Unlock()
		probe.finalized[input.Name], probe.finalizing[input.Name] = runErr, execution
		return nil
	}))
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(runtime.Stop)

	require.NoError(t, runtime.Enqueue(ctx, "test.runtime.removed", runtimeProbeInput{Name: "removed"}, servertask.EnqueueOptions{WorkspaceID: removed, MaxAttempts: 3}))
	require.Eventually(t, func() bool {
		_, _, finalized := probe.finalizedWith("removed")
		return finalized
	}, 10*time.Second, 20*time.Millisecond, "removed workspace task finalized")
	_, removedErr, _ := probe.finalizedWith("removed")
	require.Contains(t, removedErr, "is removed")
	require.Empty(t, probe.executions("removed"), "task ran for a removed workspace")
}
