//go:build server

package integrationtest

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/integration/control"
	"uuid"
)

// TestServerErrorLog 验证服务端错误记录：只写入 Error 级别日志，error、operation、action、queue 与上报的事件编号写入独立字段，其余属性按分组路径展开；列表按时间倒序返回，过期记录被清理。
func TestServerErrorLog(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	ctx := context.Background()
	instanceID := uuid.NewV7().String()
	log := platformaction.NewServerErrorLog(db, instanceID, "host-a", "v1.0.0")
	log.Start()
	// 与服务端一致，上报处理器包在错误记录外层，上报开关只对前两条 Error 日志开启。
	controlServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"id":"0"}`))
	}))
	t.Cleanup(controlServer.Close)
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := control.New(controlServer.URL, "v1.0.0", func(context.Context) (control.Identity, error) {
		return control.Identity{ServerID: "server-1", PrivateKey: privateKey}, nil
	})
	reported := 0
	reporter, err := client.NewErrorReporter(func() bool { reported++; return reported <= 2 })
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(reporter.Handler(log.Handler(slog.NewTextHandler(io.Discard, nil))))

	logger.Warn("可恢复的问题", "error", errors.New("ignored"))
	logger.With("operation", "ListTeams").Error("业务调用失败", "error", errors.New("relation does not exist"), "workspace_id", "w-1")
	logger.WithGroup("request").With("path", "/api").Error("后台任务最终失败", slog.Group("detail", "attempt", 3), "action", "agent.run")
	logger.Error("异步任务执行失败", "queue", "default", "action", "agent.run")
	reporter.Flush(5 * time.Second)
	log.Stop()

	query := platformaction.NewServerErrorListQuery(db)
	output, err := query.Execute(ctx, platformaction.ServerErrorListInput{Page: 1, PageSize: 50})
	if err != nil || output.Page.Total != 3 || len(output.Errors) != 3 {
		t.Fatalf("output = %#v, err = %v", output, err)
	}
	latest, grouped, failed := output.Errors[0], output.Errors[1], output.Errors[2]
	if latest.Message != "异步任务执行失败" || *latest.Queue != "default" || *latest.Action != "agent.run" || latest.Error != nil || latest.EventID != nil || len(latest.Attributes) != 0 {
		t.Fatalf("latest = %#v", latest)
	}
	// 分组内的属性不作为独立字段，按分组路径写入 attributes；分组内的日志同样带上上报的事件编号。
	if grouped.Action != nil || grouped.EventID == nil || len(grouped.Attributes) != 3 || grouped.Attributes["request.path"] != "/api" ||
		grouped.Attributes["request.detail.attempt"] != "3" || grouped.Attributes["request.action"] != "agent.run" {
		t.Fatalf("grouped = %#v", grouped)
	}
	if failed.InstanceID != instanceID || failed.Hostname != "host-a" || failed.Version != "v1.0.0" || *failed.Operation != "ListTeams" ||
		*failed.Error != "relation does not exist" || failed.EventID == nil || *failed.EventID == *grouped.EventID || len(failed.Attributes) != 1 || failed.Attributes["workspace_id"] != "w-1" {
		t.Fatalf("failed = %#v", failed)
	}

	// 超过保留时长的记录不再列出，并由清理删除。
	if _, err := db.NewRaw("UPDATE server_errors SET occurred_at = now() - interval '8 days' WHERE id = ?", failed.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if output, err := query.Execute(ctx, platformaction.ServerErrorListInput{Page: 1, PageSize: 50}); err != nil || output.Page.Total != 2 {
		t.Fatalf("output = %#v, err = %v", output, err)
	}
	if err := platformaction.PruneServerErrors(ctx, db); err != nil {
		t.Fatal(err)
	}
	if count, err := db.NewSelect().TableExpr("server_errors").Count(ctx); err != nil || count != 2 {
		t.Fatalf("count = %d, err = %v", count, err)
	}
}
