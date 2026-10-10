//go:build server

package integrationtest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"

	"uuid"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTaskMonitor 返回固定的任务队列概况与最终失败任务，FailedRuns 按 limit 与 offset 分页。
type fakeTaskMonitor struct {
	queues  []servertask.QueueStatus
	delayed int
	failed  []servertask.FailedRun
}

// TaskStatus 返回固定的队列概况与推迟执行的任务数。
func (m fakeTaskMonitor) TaskStatus(context.Context) (servertask.TaskStatus, error) {
	return servertask.TaskStatus{Queues: m.queues, Delayed: m.delayed}, nil
}

// FailedRuns 返回固定失败任务中的一页及总数，limit 为 0 时返回 offset 之后的全部。
func (m fakeTaskMonitor) FailedRuns(_ context.Context, limit, offset int) ([]servertask.FailedRun, int, error) {
	runs := m.failed[min(offset, len(m.failed)):]
	if limit > 0 {
		runs = runs[:min(limit, len(runs))]
	}
	return runs, len(m.failed), nil
}

// TestPlatformServerInstances 验证服务端进程心跳：登记与刷新同一条记录，心跳超时的进程标为失联并排在后面，失联超过保留时长的记录在其他进程心跳时删除，正常退出时删除本进程记录。
func TestPlatformServerInstances(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "运行状态", DisplayName: "管理员", Email: servertest.UniqueEmail("runtime"), Password: "password123"})
	queues := []servertask.QueueStatus{{Queue: servertask.QueueAgent, Waiting: 2, Running: 1, Failed: 3}}
	query := platformaction.NewRuntimeStatusQuery(db, fakeTaskMonitor{queues: queues, delayed: 3})

	first, lost, expired := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	for _, id := range []string{lost, expired} {
		err := serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{
			ID: id, Hostname: "old-host", Version: "v0.9.0", BusDriver: clusterbus.DriverNATS, BusConnected: true,
		})
		require.NoError(t, err)
	}
	// 一个进程 5 分钟没有心跳，另一个超过 1 小时。
	_, err := db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '5 minutes' WHERE id = ?", lost).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '2 hours' WHERE id = ?", expired).Exec(ctx)
	require.NoError(t, err)
	report := serverinstanceaction.InstanceReport{ID: first, Hostname: "host-a", Version: "v1.0.0", BusDriver: clusterbus.DriverPostgres, BusConnected: true}
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, report))
	// 再次心跳只刷新连接状态、消息总线负载与心跳时间，保留启动时间。
	report.BusConnected, report.BusMessageRate, report.BusFailures = false, 12.5, 3
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, report))
	status, err := query.Execute(ctx)
	require.NoError(t, err)
	require.Len(t, status.Servers, 2)
	online, offline := status.Servers[0], status.Servers[1]
	type serverState struct {
		ID                        string
		Online, BusConnected      bool
		Hostname, Version, Driver string
		Rate                      float64
		Failures                  int
	}
	require.Equal(t, serverState{ID: first, Online: true, Hostname: "host-a", Version: "v1.0.0", Driver: "postgres", Rate: 12.5, Failures: 3},
		serverState{online.ID, online.Online, online.BusConnected, online.Hostname, online.Version, online.BusDriver, online.BusMessageRate, online.BusFailures})
	require.False(t, online.HeartbeatAt.Before(online.StartedAt))
	require.Equal(t, lost, offline.ID)
	require.False(t, offline.Online)
	require.True(t, offline.BusConnected)
	require.Equal(t, "nats", offline.BusDriver)
	require.Equal(t, queues, status.Queues, "queue statuses")
	require.Equal(t, 3, status.DelayedTasks, "delayed tasks")

	require.NoError(t, serverinstanceaction.RemoveInstance(ctx, db, first))
	status, err = query.Execute(ctx)
	require.NoError(t, err)
	require.Len(t, status.Servers, 1)
	require.Equal(t, lost, status.Servers[0].ID)
	require.GreaterOrEqual(t, status.NotifyQueueUsage, 0.0)
}

// TestPlatformFailedTasks 验证失败任务列表按页读取任务运行时的最终失败任务，并补充所属工作区名称，平台级任务没有工作区名称。
func TestPlatformFailedTasks(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "失败任务", DisplayName: "管理员", Email: servertest.UniqueEmail("failed-tasks"), Password: "password123"})
	workspaceID := installed.Identity.Workspace.ID
	failedAt := time.Now().UTC()
	runs := []servertask.FailedRun{
		{ID: "run-1", WorkspaceID: &workspaceID, ActionName: "knowledge.document.process", QueueName: servertask.QueueKnowledge, Attempt: 3, LastError: "upstream returned 502", FailedAt: failedAt},
		{ID: "run-2", ActionName: "license.sync", QueueName: servertask.QueueMaintenance, Attempt: 5, LastError: "timeout", FailedAt: failedAt.Add(-time.Minute)},
		{ID: "run-3", WorkspaceID: &workspaceID, ActionName: "agent.run", QueueName: servertask.QueueAgent, Attempt: 1, LastError: "permanent", FailedAt: failedAt.Add(-2 * time.Minute)},
	}
	query := platformaction.NewFailedTaskListQuery(db, fakeTaskMonitor{failed: runs})
	first, err := query.Execute(ctx, platformaction.FailedTaskListInput{Page: 1, PageSize: 2})
	require.NoError(t, err)
	require.Equal(t, 3, first.Page.Total)
	require.Len(t, first.Tasks, 2)
	require.Equal(t, runs[0], first.Tasks[0].FailedRun)
	require.Equal(t, installed.Identity.Workspace.Name, *first.Tasks[0].WorkspaceName)
	require.Equal(t, runs[1], first.Tasks[1].FailedRun)
	require.Nil(t, first.Tasks[1].WorkspaceName, "platform task workspace name")
	second, err := query.Execute(ctx, platformaction.FailedTaskListInput{Page: 2, PageSize: 2})
	require.NoError(t, err)
	require.Len(t, second.Tasks, 1)
	require.Equal(t, runs[2], second.Tasks[0].FailedRun)
}

// TestFenceLostInstances 验证心跳超过隔离时限的服务端进程的数据库连接被终止，仍有连接的过期记录不被心跳删除，在线、刚失联未超过时限的进程与本进程的连接保留。
func TestFenceLostInstances(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	self, live, recent, lost := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	for _, id := range []string{self, live, recent, lost} {
		require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{ID: id, Hostname: "host", Version: "v1.0.0", BusDriver: clusterbus.DriverPostgres}))
	}
	_, err := db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '3 minutes' WHERE id IN (?, ?)", self, lost).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '1 minute' WHERE id = ?", recent).Exec(ctx)
	require.NoError(t, err)
	// 每个进程各占一条以其实例名称命名的连接。
	conns := map[string]*sql.Conn{}
	for _, id := range []string{self, live, recent, lost} {
		conn, err := db.DB.Conn(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		_, err = conn.ExecContext(ctx, "SELECT set_config('application_name', $1, false)", serverinstanceaction.InstanceApplicationName(id))
		require.NoError(t, err)
		conns[id] = conn
	}

	// 失联超过保留时长但仍有连接的记录不在心跳时删除，留待隔离。
	_, err = db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '2 hours' WHERE id = ?", lost).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{ID: live, Hostname: "host", Version: "v1.0.0", BusDriver: clusterbus.DriverPostgres}))
	terminated, err := serverinstanceaction.FenceLostInstances(ctx, db, self)
	require.NoError(t, err)
	require.Equal(t, 1, terminated)
	_, err = conns[lost].ExecContext(ctx, "SELECT 1")
	require.Error(t, err)
	for _, id := range []string{self, live, recent} {
		_, err = conns[id].ExecContext(ctx, "SELECT 1")
		require.NoError(t, err)
	}
	// 连接终止后，下一次心跳删除该过期记录。
	require.Eventually(t, func() bool {
		if err := serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{ID: live, Hostname: "host", Version: "v1.0.0", BusDriver: clusterbus.DriverPostgres}); !assert.NoError(t, err) {
			return false
		}
		exists, err := db.NewSelect().Table("server_instances").Where("id = ?", lost).Exists(ctx)
		return assert.NoError(t, err) && !exists
	}, 5*time.Second, 100*time.Millisecond)
}
