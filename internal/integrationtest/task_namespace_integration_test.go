//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/jetq/jetqtest"
	"github.com/runforyou-ai/luway/internal/servertest"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
)

// TestTaskNamespaces 验证同一 NATS 上含连字符与下划线的命名空间分别投递和执行任务，各自维护相同的去重键。
func TestTaskNamespaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	db := store.DB()
	workspaceID := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "任务命名空间", DisplayName: "管理员", Email: servertest.UniqueEmail("task-namespaces"), Password: "password123",
	}).Identity.Workspace.ID
	nats := jetqtest.Start(t)
	probe := &runtimeProbe{runs: map[string][]servertask.Execution{}}
	release := make(chan struct{})
	runtimes := make([]struct {
		namespace  string
		instanceID string
		runtime    *servertask.Runtime
	}, 2)
	for index, namespace := range []string{"test-deployment", "test_deployment"} {
		item := &runtimes[index]
		item.namespace, item.instanceID = namespace, uuid.NewV7().String()
		item.runtime, err = servertask.New(ctx, nats.JetStream, servertask.Options{Namespace: namespace, Replicas: 1}, db, db, item.instanceID)
		require.NoError(t, err)
		require.NoError(t, item.runtime.Registry().RegisterJSON("test.namespace.probe", func(ctx context.Context, input runtimeProbeInput) error {
			probe.record(ctx, input.Name)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
	}
	for _, item := range runtimes {
		require.NoError(t, item.runtime.Start(ctx))
		t.Cleanup(item.runtime.Stop)
	}
	// 两个任务同时保持执行状态，各命名空间独立占用同名去重键。
	t.Cleanup(func() { close(release) })
	for _, item := range runtimes {
		require.NoError(t, item.runtime.Enqueue(ctx, "test.namespace.probe", runtimeProbeInput{Name: item.namespace}, servertask.EnqueueOptions{
			WorkspaceID: workspaceID, IdempotencyKey: "shared-key",
		}))
	}
	for _, item := range runtimes {
		require.Eventually(t, func() bool { return len(probe.executions(item.namespace)) == 1 }, 5*time.Second, 20*time.Millisecond, item.namespace)
		execution := probe.executions(item.namespace)[0]
		require.Equal(t, item.instanceID, execution.InstanceID)
		require.Equal(t, 1, execution.Attempt)
	}
}
