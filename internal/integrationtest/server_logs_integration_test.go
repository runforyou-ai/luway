//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"uuid"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverlogaction "github.com/runforyou-ai/luway/internal/actions/serverlog"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestServerLog 验证服务端日志：全部级别写入数据库，控制台只输出不低于其级别的日志；error、上报的事件编号与日志作用域写入独立字段，其余属性按分组路径展开；列表按时间倒序返回并支持筛选与游标分页。
func TestServerLog(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	instanceID := uuid.NewV7().String()
	logs := serverlogaction.NewServerLog(instanceID, "host-a", "v1.0.0")
	// 与服务端一致，上报处理器包在日志记录外层，上报开关只对第一条 Error 日志开启。
	controlServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"id":"0"}`))
	}))
	t.Cleanup(controlServer.Close)
	_, privateKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	client := control.New(controlServer.URL, "v1.0.0", func(context.Context) (control.Identity, error) {
		return control.Identity{ServerID: "server-1", PrivateKey: privateKey}, nil
	})
	reported := 0
	reporter, err := client.NewErrorReporter("instance-1", func() bool { reported++; return reported <= 1 })
	require.NoError(t, err)
	var console bytes.Buffer
	logger := slog.New(reporter.Handler(logs.Handler(slog.NewTextHandler(&console, &slog.HandlerOptions{Level: slog.LevelInfo}))))

	// 开始写入前排队的日志在开始后写入。
	request := logscope.With(ctx, logscope.Scope{TraceID: "trace-1", Operation: "ListTeams", WorkspaceID: "w-1", AccountID: "a-1"})
	logger.DebugContext(request, "调试细节", "team_id", "t-1")
	require.NoError(t, logs.Start(ctx, db))
	logger.InfoContext(request, "团队已读取")
	logger.ErrorContext(request, "业务调用失败", "error", errors.New("relation does not exist"))
	task := logscope.With(ctx, logscope.Scope{TraceID: "trace-2", TaskRunID: "run-1", Action: "agent.run", Queue: "default", WorkspaceID: "w-2"})
	logger.WithGroup("request").With("path", "/api").WarnContext(task, "任务重试", slog.Group("detail", "attempt", 3))
	reporter.Flush(5 * time.Second)
	logs.Stop()
	require.NotContains(t, console.String(), "调试细节")
	require.Contains(t, console.String(), "团队已读取")

	query := platformaction.NewServerLogListQuery(db)
	output, err := query.Execute(ctx, platformaction.ServerLogListInput{})
	require.NoError(t, err)
	require.Len(t, output.Logs, 4)
	require.Empty(t, output.NextCursor)
	warned, failed, informed, debugged := output.Logs[0], output.Logs[1], output.Logs[2], output.Logs[3]
	require.Equal(t, "调试细节", debugged.Message)
	require.Equal(t, int(slog.LevelDebug), debugged.Level)
	require.Equal(t, map[string]string{"team_id": "t-1"}, debugged.Attributes)
	require.Equal(t, int(slog.LevelInfo), informed.Level)
	require.Equal(t, instanceID, failed.InstanceID)
	require.Equal(t, "host-a", failed.Hostname)
	require.Equal(t, "v1.0.0", failed.Version)
	require.Equal(t, "trace-1", *failed.TraceID)
	require.Equal(t, "ListTeams", *failed.Operation)
	require.Equal(t, "w-1", *failed.WorkspaceID)
	require.Equal(t, "a-1", *failed.AccountID)
	require.Equal(t, "relation does not exist", *failed.Error)
	require.NotNil(t, failed.EventID)
	require.Empty(t, failed.Attributes)
	require.Equal(t, int(slog.LevelWarn), warned.Level)
	require.Equal(t, "run-1", *warned.TaskRunID)
	require.Equal(t, "agent.run", *warned.Action)
	require.Equal(t, "default", *warned.Queue)
	require.Nil(t, warned.EventID)
	require.Equal(t, map[string]string{"request.path": "/api", "request.detail.attempt": "3"}, warned.Attributes)

	// 按最低级别、串联编号、业务入口、工作区与服务器筛选。
	warnLevel := int(slog.LevelWarn)
	for name, item := range map[string]struct {
		input platformaction.ServerLogListInput
		want  []string
	}{
		"最低级别": {platformaction.ServerLogListInput{MinLevel: &warnLevel}, []string{warned.ID, failed.ID}},
		"串联编号": {platformaction.ServerLogListInput{TraceID: "trace-1"}, []string{failed.ID, informed.ID, debugged.ID}},
		"业务入口": {platformaction.ServerLogListInput{Entry: "ListTeams"}, []string{failed.ID, informed.ID, debugged.ID}},
		"后台任务": {platformaction.ServerLogListInput{Entry: "agent.run"}, []string{warned.ID}},
		"工作区":  {platformaction.ServerLogListInput{WorkspaceID: "w-2"}, []string{warned.ID}},
		"服务器":  {platformaction.ServerLogListInput{InstanceID: uuid.NewV7().String()}, []string{}},
	} {
		filtered, err := query.Execute(ctx, item.input)
		require.NoError(t, err, name)
		ids := make([]string, 0, len(filtered.Logs))
		for _, record := range filtered.Logs {
			ids = append(ids, record.ID)
		}
		require.Equal(t, item.want, ids, name)
	}

	// 游标逐页读取，页与页之间不重叠。
	first, err := query.Execute(ctx, platformaction.ServerLogListInput{PageSize: 3})
	require.NoError(t, err)
	require.Len(t, first.Logs, 3)
	require.NotEmpty(t, first.NextCursor)
	second, err := query.Execute(ctx, platformaction.ServerLogListInput{PageSize: 3, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Logs, 1)
	require.Equal(t, debugged.ID, second.Logs[0].ID)
	require.Empty(t, second.NextCursor)
	_, err = query.Execute(ctx, platformaction.ServerLogListInput{Cursor: "invalid"})
	require.Error(t, err)
	// 服务器编号只接受规范的小写 UUID。
	_, err = query.Execute(ctx, platformaction.ServerLogListInput{InstanceID: "urn:uuid:" + instanceID})
	require.Error(t, err)
}

// TestServerLogPartitions 验证分区维护按数据库时钟提前创建今天起的日分区、删除整天超出保留期的分区，列表不返回保留期外的日志。
func TestServerLogPartitions(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	require.NoError(t, serverlogaction.MaintainServerLogPartitions(ctx, db))
	var today time.Time
	require.NoError(t, db.NewRaw("SELECT (now() AT TIME ZONE 'UTC')::date").Scan(ctx, &today))
	partitions := serverLogPartitions(t, db)
	for offset := range 3 {
		require.Contains(t, partitions, "server_logs_"+today.AddDate(0, 0, offset).Format("20060102"))
	}

	// 保留期边界之外的一天建立分区并写入日志，列表不返回，维护后整个分区被删除；边界当天的分区保留。
	expiredDay := today.AddDate(0, 0, -serverlogaction.ServerLogRetentionDays-1)
	keptDay := today.AddDate(0, 0, -serverlogaction.ServerLogRetentionDays)
	for _, day := range []time.Time{expiredDay, keptDay} {
		_, err := db.ExecContext(ctx, "CREATE TABLE ? PARTITION OF server_logs FOR VALUES FROM (?) TO (?)",
			bun.Ident("server_logs_"+day.Format("20060102")), day, day.AddDate(0, 0, 1))
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO server_logs (id, occurred_at, level, instance_id, hostname, version, message, attributes)
		VALUES (?, ?, 0, ?, 'host-a', 'v1.0.0', '过期日志', '{}')
	`, uuid.NewV7().String(), expiredDay.Add(time.Hour), uuid.NewV7().String())
	require.NoError(t, err)
	output, err := platformaction.NewServerLogListQuery(db).Execute(ctx, platformaction.ServerLogListInput{})
	require.NoError(t, err)
	require.Empty(t, output.Logs)

	require.NoError(t, serverlogaction.MaintainServerLogPartitions(ctx, db))
	partitions = serverLogPartitions(t, db)
	require.NotContains(t, partitions, "server_logs_"+expiredDay.Format("20060102"))
	require.Contains(t, partitions, "server_logs_"+keptDay.Format("20060102"))
}

// serverLogPartitions 返回服务端日志表现有的分区名。
func serverLogPartitions(t *testing.T, db *bun.DB) []string {
	t.Helper()
	var partitions []string
	require.NoError(t, db.NewRaw(`
		SELECT child.relname FROM pg_inherits
		JOIN pg_class child ON child.oid = pg_inherits.inhrelid
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		WHERE parent.relname = 'server_logs'
	`).Scan(context.Background(), &partitions))
	return partitions
}
