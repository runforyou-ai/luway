//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mcpserveraction "github.com/runforyou-ai/cervi/internal/actions/mcpserver"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	mcpintegration "github.com/runforyou-ai/cervi/internal/integration/mcp"
	servertest "github.com/runforyou-ai/cervi/internal/servertest"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// TestMCPServerLifecycle 验证 MCP 配置的增删改查、名称唯一性和企业隔离。
func TestMCPServerLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	identities := make([]*servermodels.Identity, 0, 2)
	for range 2 {
		installed := installWorkspace(t, db, workspaceSpec{
			Name: "MCP 测试", DisplayName: "维护人员", Email: uniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
		})
		identities = append(identities, installed.Identity)
	}
	owner, other := identities[0], identities[1]
	client := mcpDiscoverFunc(func(context.Context, mcpintegration.Config) ([]domain.MCPTool, error) {
		return []domain.MCPTool{{Name: "search", Description: "检索文档"}}, nil
	})
	test := mcpserveraction.NewTestConnectionAction(client)
	create := mcpserveraction.NewCreateMCPServerAction(db, test)
	get := mcpserveraction.NewGetMCPServerQuery(db)
	list := mcpserveraction.NewListMCPServersQuery(db)
	update := mcpserveraction.NewUpdateMCPServerAction(db, test)
	remove := mcpserveraction.NewDeleteMCPServerAction(db)
	input := mcpserveraction.Input{Name: " Docs ", URL: " https://example.com/mcp ", ServerType: domain.MCPServerTypeStreamableHTTP, AuthorizationToken: "test-token"}
	created, err := create.Execute(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Docs" || created.URL != "https://example.com/mcp" {
		t.Fatalf("unexpected normalization: %+v", created)
	}
	read, err := get.Execute(ctx, owner, created.ID)
	if err != nil || read.AuthorizationToken != input.AuthorizationToken || read.ServerType != input.ServerType {
		t.Fatalf("read = %+v, error = %v", read, err)
	}
	input.Name = "docs"
	_, err = create.Execute(ctx, owner, input)
	var fields *common.FieldError
	if !errors.As(err, &fields) || fields.Fields["name"] != mcpserveraction.ValidationNameDuplicate {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := create.Execute(ctx, other, input); err != nil {
		t.Fatalf("same name in other organization: %v", err)
	}
	if _, err := get.Execute(ctx, other, created.ID); !errors.Is(err, mcpserveraction.ErrNotFound) {
		t.Fatalf("cross-organization read: %v", err)
	}
	if _, err := update.Execute(ctx, other, created.ID, input); !errors.Is(err, mcpserveraction.ErrNotFound) {
		t.Fatalf("cross-organization update: %v", err)
	}
	if err := remove.Execute(ctx, other, created.ID); !errors.Is(err, mcpserveraction.ErrNotFound) {
		t.Fatalf("cross-organization delete: %v", err)
	}
	records, err := list.Execute(ctx, owner)
	if err != nil || len(records) != 1 || records[0].ID != created.ID {
		t.Fatalf("list = %+v, error = %v", records, err)
	}
	input.Name, input.URL, input.ServerType, input.AuthorizationToken = "Updated", "http://localhost:8080/sse", domain.MCPServerTypeSSE, ""
	saved, err := update.Execute(ctx, owner, created.ID, input)
	if err != nil || saved.Name != input.Name || saved.URL != input.URL || saved.ServerType != input.ServerType || saved.AuthorizationToken != "" {
		t.Fatalf("update = %+v, error = %v", saved, err)
	}
	input.Name = "Another"
	second, err := create.Execute(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "UPDATED"
	if _, err = update.Execute(ctx, owner, second.ID, input); !errors.As(err, &fields) || fields.Fields["name"] != mcpserveraction.ValidationNameDuplicate {
		t.Fatalf("duplicate update: %v", err)
	}
	if err := remove.Execute(ctx, owner, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := get.Execute(ctx, owner, created.ID); !errors.Is(err, mcpserveraction.ErrNotFound) {
		t.Fatalf("read deleted: %v", err)
	}
	if err := remove.Execute(ctx, owner, second.ID); err != nil {
		t.Fatal(err)
	}
	records, err = list.Execute(ctx, owner)
	if err != nil || records == nil || len(records) != 0 {
		t.Fatalf("empty list = %+v, error = %v", records, err)
	}
}

// mcpDiscoverFunc 为工具更新测试提供可控的远端响应。
type mcpDiscoverFunc func(context.Context, mcpintegration.Config) ([]domain.MCPTool, error)

// Discover 返回测试指定的工具目录或错误。
func (f mcpDiscoverFunc) Discover(ctx context.Context, config mcpintegration.Config) ([]domain.MCPTool, error) {
	return f(ctx, config)
}

// TestMCPToolsUpdates 验证保存写入探测目录、批量更新去重、整批替换、失败保留及旧批次拒绝。
func TestMCPToolsUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "工具测试", DisplayName: "维护人员", Email: uniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	identity := installed.Identity
	tools := []domain.MCPTool{{Name: "search", Description: "查找文档"}, {Name: "read", Description: "读取文档"}}
	var discoverError error
	client := mcpDiscoverFunc(func(context.Context, mcpintegration.Config) ([]domain.MCPTool, error) { return tools, discoverError })
	tasks := newTestTasks(db)
	worker := mcpserveraction.NewUpdateToolsAction(db, client)
	if err := tasks.Registry().RegisterJSONWithTerminalFailure(mcpserveraction.RefreshToolsActionName, worker.Execute, worker.FinalizeFailure); err != nil {
		t.Fatal(err)
	}
	probe := mcpserveraction.NewTestConnectionAction(client)
	create := mcpserveraction.NewCreateMCPServerAction(db, probe)
	update := mcpserveraction.NewUpdateMCPServerAction(db, probe)
	refresh := mcpserveraction.NewRefreshToolsAction(db, mcpserveraction.NewToolsScheduler(tasks))
	get := mcpserveraction.NewGetMCPServerQuery(db)
	input := mcpserveraction.Input{Name: "Docs", URL: "https://example.com/mcp", ServerType: domain.MCPServerTypeStreamableHTTP}
	record, err := create.Execute(ctx, identity, input)
	if err != nil {
		t.Fatal(err)
	}
	if record.ToolsUpdating || record.ToolsUpdatedAt == nil || len(record.Tools) != 2 {
		t.Fatalf("create must save probed tools: %+v", record)
	}
	if err := refresh.Execute(ctx, identity); err != nil {
		t.Fatal(err)
	}
	first := mcpRefreshInput(t, ctx, db, record.ID)
	if err := refresh.Execute(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if current := mcpRefreshInput(t, ctx, db, record.ID); current != first {
		t.Fatal("refresh duplicated pending task")
	}
	mark := mcpserveraction.NewUpdateToolPurposeAction(db)
	for name, purpose := range map[string]domain.MCPToolPurpose{"search": domain.MCPToolPurposeQuery, "read": domain.MCPToolPurposeAction} {
		if _, err := mark.Execute(ctx, identity, record.ID, mcpserveraction.ToolPurposeInput{ToolName: name, Purpose: purpose}); err != nil {
			t.Fatal(err)
		}
	}
	// 保存写入新目录、结束进行中的更新，并只保留仍存在的工具的用途标记。
	tools = []domain.MCPTool{{Name: "search", Description: "查找文档"}}
	input.Name, input.CustomerScoped = "Renamed", true
	record, err = update.Execute(ctx, identity, record.ID, input)
	if err != nil || record.ToolsUpdating || !record.CustomerScoped || len(record.Tools) != 1 {
		t.Fatalf("save must write probed tools: %+v, %v", record, err)
	}
	if len(record.ToolPurposes) != 1 || record.ToolPurposes["search"] != domain.MCPToolPurposeQuery {
		t.Fatalf("save must keep purposes of remaining tools: %+v", record)
	}
	// 保存前投递的旧批次拒绝写回。
	tools = []domain.MCPTool{{Name: "stale", Description: "旧目录"}}
	if err := worker.Execute(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := worker.FinalizeFailure(ctx, first, errors.New("old worker")); err != nil {
		t.Fatal(err)
	}
	record, _ = get.Execute(ctx, identity, record.ID)
	if len(record.Tools) != 1 || record.Tools[0].Name != "search" || record.ToolsFailure != "" {
		t.Fatalf("old batch changed tools: %+v", record)
	}
	// 更换地址后清除工具用途。
	tools = []domain.MCPTool{{Name: "search", Description: "查找文档"}}
	input.URL = "https://example.com/other-mcp"
	record, err = update.Execute(ctx, identity, record.ID, input)
	if err != nil || len(record.ToolPurposes) != 0 {
		t.Fatalf("URL change must clear purposes: %+v, %v", record, err)
	}
	tools = []domain.MCPTool{}
	if err := refresh.Execute(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := worker.Execute(ctx, mcpRefreshInput(t, ctx, db, record.ID)); err != nil {
		t.Fatal(err)
	}
	record, _ = get.Execute(ctx, identity, record.ID)
	if record.ToolsUpdating || record.ToolsUpdatedAt == nil || len(record.Tools) != 0 || len(record.ToolPurposes) != 0 {
		t.Fatalf("empty list must replace tools: %+v", record)
	}
	// 保存探测失败时，配置和目录均保持原样。
	discoverError = connectiontest.NewError(connectiontest.StageAuthenticate, connectiontest.FailureUnauthorized, nil)
	input.Name = "Must not save"
	if _, err := update.Execute(ctx, identity, record.ID, input); err == nil {
		t.Fatal("failed probe saved configuration")
	}
	if _, err := create.Execute(ctx, identity, input); err == nil {
		t.Fatal("failed probe created configuration")
	}
	record, _ = get.Execute(ctx, identity, record.ID)
	if record.Name != "Renamed" || record.ToolsUpdating {
		t.Fatalf("failed save changed record: %+v", record)
	}
	if err := refresh.Execute(ctx, identity); err != nil {
		t.Fatal(err)
	}
	failed := mcpRefreshInput(t, ctx, db, record.ID)
	if err := worker.Execute(ctx, failed); err == nil {
		t.Fatal("expected failed discovery")
	}
	if err := worker.FinalizeFailure(ctx, failed, discoverError); err != nil {
		t.Fatal(err)
	}
	record, _ = get.Execute(ctx, identity, record.ID)
	if record.ToolsUpdating || record.ToolsFailure != string(connectiontest.FailureUnauthorized) || record.ToolsUpdatedAt == nil {
		t.Fatalf("failure did not preserve snapshot: %+v", record)
	}
	// 拒绝删除后的任务写回。
	discoverError = nil
	if err := refresh.Execute(ctx, identity); err != nil {
		t.Fatal(err)
	}
	pending := mcpRefreshInput(t, ctx, db, record.ID)
	if err := mcpserveraction.NewDeleteMCPServerAction(db).Execute(ctx, identity, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.Execute(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := worker.FinalizeFailure(ctx, pending, errors.New("deleted")); err != nil {
		t.Fatal(err)
	}
}

// mcpRefreshInput 读取服务当前批次对应的真实持久化任务。
func mcpRefreshInput(t *testing.T, ctx context.Context, db *bun.DB, serverID string) mcpserveraction.RefreshToolsInput {
	t.Helper()
	var payload string
	err := db.NewRaw(`SELECT tr.payload FROM task_runs tr JOIN mcp_servers ms ON tr.payload->>'refreshId' = ms.tools_refresh_id::text WHERE ms.id = ?`, serverID).Scan(ctx, &payload)
	if err != nil {
		t.Fatal(err)
	}
	var input mcpserveraction.RefreshToolsInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatal(err)
	}
	return input
}
