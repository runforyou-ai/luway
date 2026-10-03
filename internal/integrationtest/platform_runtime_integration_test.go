//go:build server

package integrationtest

import (
	"context"
	"testing"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"uuid"
)

// TestPlatformServerInstances 验证服务端进程心跳：登记与刷新同一条记录，心跳超时的进程标为失联并排在后面，失联超过保留时长的记录在其他进程心跳时删除，正常退出时删除本进程记录。
func TestPlatformServerInstances(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	ctx := context.Background()
	installWorkspace(t, db, workspaceSpec{Name: "运行状态", DisplayName: "管理员", Email: uniqueEmail("runtime"), Password: "password123"})
	query := platformaction.NewRuntimeStatusQuery(db, serverfilecontent.S3Config{})

	first, lost, expired := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	for _, id := range []string{lost, expired} {
		if err := platformaction.ReportInstance(ctx, db, platformaction.InstanceReport{
			ID: id, Hostname: "old-host", Version: "v0.9.0", TasksNATSConnected: true, RealtimeNATSConnected: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 一个进程 5 分钟没有心跳，另一个超过 1 小时。
	if _, err := db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '5 minutes' WHERE id = ?", lost).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw("UPDATE server_instances SET heartbeat_at = now() - interval '2 hours' WHERE id = ?", expired).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	report := platformaction.InstanceReport{ID: first, Hostname: "host-a", Version: "v1.0.0", TasksNATSConnected: true, RealtimeNATSConnected: true}
	if err := platformaction.ReportInstance(ctx, db, report); err != nil {
		t.Fatal(err)
	}
	// 再次心跳只刷新连接状态与心跳时间，保留启动时间。
	report.RealtimeNATSConnected = false
	if err := platformaction.ReportInstance(ctx, db, report); err != nil {
		t.Fatal(err)
	}
	status, err := query.Execute(ctx)
	if err != nil || len(status.Servers) != 2 {
		t.Fatalf("servers = %#v, err = %v", status.Servers, err)
	}
	online, offline := status.Servers[0], status.Servers[1]
	if online.ID != first || !online.Online || !online.TasksNATSConnected || online.RealtimeNATSConnected || online.Hostname != "host-a" || online.Version != "v1.0.0" ||
		online.HeartbeatAt.Before(online.StartedAt) {
		t.Fatalf("online server = %#v", online)
	}
	if offline.ID != lost || offline.Online || !offline.RealtimeNATSConnected {
		t.Fatalf("lost server = %#v", offline)
	}
	if status.ObjectStorage.Enabled || status.ObjectStorage.Error != "" {
		t.Fatalf("object storage = %#v", status.ObjectStorage)
	}

	if err := platformaction.RemoveInstance(ctx, db, first); err != nil {
		t.Fatal(err)
	}
	if status, err := query.Execute(ctx); err != nil || len(status.Servers) != 1 || status.Servers[0].ID != lost {
		t.Fatalf("servers after remove = %#v, err = %v", status.Servers, err)
	}

	// 开启对象存储时实时检查存储桶，无法访问时给出原因。
	unreachable := platformaction.NewRuntimeStatusQuery(db, serverfilecontent.S3Config{
		Enabled: true, Endpoint: "http://127.0.0.1:1", Region: "us-east-1", Bucket: "files", AccessKeyID: "key", SecretAccessKey: "secret", ForcePathStyle: true,
	})
	if status, err := unreachable.Execute(ctx); err != nil || !status.ObjectStorage.Enabled || status.ObjectStorage.Error == "" {
		t.Fatalf("unreachable object storage = %#v, err = %v", status.ObjectStorage, err)
	}
}
