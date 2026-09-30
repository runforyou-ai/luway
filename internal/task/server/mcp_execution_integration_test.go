//go:build server

package server_test

import (
	"context"
	"errors"
	mcpserveraction "github.com/runforyou-ai/cervi/internal/actions/mcpserver"
	"github.com/runforyou-ai/cervi/internal/domain"
	mcpintegration "github.com/runforyou-ai/cervi/internal/integration/mcp"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"testing"
	"uuid"
)

// mcpExecutionClient 提供任务归属验证使用的固定目录。
type mcpExecutionClient struct{}

// Discover 返回固定目录，不访问外部服务。
func (mcpExecutionClient) Discover(context.Context, mcpintegration.Config) ([]domain.MCPTool, error) {
	return []domain.MCPTool{{Name: "search"}}, nil
}

// TestMCPToolsFenceWorker 验证旧 Worker 无法写回，租约耗尽后能结束更新状态。
func TestMCPToolsFenceWorker(t *testing.T) {
	ctx, db, tasks := servertask.NewExecutionRuntimeForTest(t)
	refreshID := uuid.NewV7().String()
	record := servermodels.MCPServer{OrganizationID: uuid.NewV7().String(), Name: "Worker test", URL: "https://example.com/mcp", ServerType: domain.MCPServerTypeStreamableHTTP, ToolsRefreshID: &refreshID}
	if _, err := db.NewInsert().Model(&record).Column("organization_id", "name", "url", "server_type", "tools_refresh_id").Returning("*").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.NewDelete().Model(&record).WherePK().Exec(context.Background()) })
	worker := mcpserveraction.NewUpdateToolsAction(db, mcpExecutionClient{})
	if err := tasks.Registry().RegisterJSONWithTerminalFailure(mcpserveraction.RefreshToolsActionName, worker.Execute, worker.FinalizeFailure); err != nil {
		t.Fatal(err)
	}
	input := mcpserveraction.RefreshToolsInput{OrganizationID: record.OrganizationID, MCPServerID: record.ID, RefreshID: refreshID}
	taskID, err := tasks.Enqueue(ctx, mcpserveraction.RefreshToolsActionName, input, servertask.EnqueueOptions{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id = ?", taskID).Exec(context.Background())
		_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", taskID).Exec(context.Background())
	})
	var current servermodels.TaskRun
	if err := db.NewUpdate().Model(&current).Set("status = 'running'").Set("attempt = 1").Set("worker_id = 'current'").Set("lease_expires_at = now() + interval '1 hour'").Where("id = ?", taskID).Returning("*").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	old := current
	oldWorker := "old"
	old.WorkerID = &oldWorker
	for _, finalize := range []bool{false, true} {
		oldCtx := servertask.WithExecutionForTest(ctx, &old, finalize, false)
		if finalize {
			err = worker.FinalizeFailure(oldCtx, input, errors.New("old worker"))
		} else {
			err = worker.Execute(oldCtx, input)
		}
		if !errors.Is(err, servertask.ErrExecutionLost) {
			t.Fatalf("old worker finalize=%v err=%v", finalize, err)
		}
		record.ToolsRefreshID = nil
		if err := db.NewSelect().Model(&record).WherePK().Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if record.ToolsRefreshID == nil || record.ToolsUpdatedAt != nil || record.ToolsFailure != "" {
			t.Fatalf("old worker changed state: %+v", record)
		}
	}
	// 模拟进程中断后任务租约耗尽，由可靠任务的终态回调收尾。
	if _, err := db.NewUpdate().Model((*servermodels.TaskRun)(nil)).Set("lease_expires_at = now() - interval '1 second'").Where("id = ?", taskID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	exhaustedCtx := servertask.WithExecutionForTest(ctx, &current, true, true)
	if err := worker.FinalizeFailure(exhaustedCtx, input, errors.New("worker interrupted")); err != nil {
		t.Fatal(err)
	}
	record.ToolsRefreshID = nil
	if err := db.NewSelect().Model(&record).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if record.ToolsRefreshID != nil || record.ToolsFailure == "" {
		t.Fatalf("exhausted task remained updating: %+v", record)
	}
}
