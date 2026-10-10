//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	businesssystemaction "github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/internal/integration/openapi"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestBusinessSystemLifecycle 验证业务系统配置的增删改查、名称唯一性和工作区隔离。
func TestBusinessSystemLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identities := make([]*servermodels.Identity, 0, 2)
	for range 2 {
		installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
			Name: "业务系统测试", DisplayName: "维护人员", Email: servertest.UniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
		})
		identities = append(identities, installed.Identity)
	}
	owner, other := identities[0], identities[1]
	client := mcpDiscoverFunc(func(_ context.Context, config mcpintegration.Config) ([]domain.BusinessTool, error) {
		assert.Contains(t, []string{"", "Bearer test-token"}, config.Headers["Authorization"], "credential headers = %+v", config.Headers)
		return []domain.BusinessTool{{Name: "search", Description: "检索文档"}}, nil
	})
	test := businesssystemaction.NewTestConnectionAction(businesssystemaction.NewConnector(client, nil))
	create := businesssystemaction.NewCreateBusinessSystemAction(db, test)
	get := businesssystemaction.NewGetBusinessSystemQuery(db)
	list := businesssystemaction.NewListBusinessSystemsQuery(db)
	update := businesssystemaction.NewUpdateBusinessSystemAction(db, test)
	remove := businesssystemaction.NewDeleteBusinessSystemAction(db)
	input := mcpSystemInput(" Docs ", " https://example.com/mcp ", domain.MCPServerTypeStreamableHTTP)
	input.Connection.Credential = domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialBearer, Token: "test-token", Username: "ignored"}
	created, err := create.Execute(ctx, owner, input)
	require.NoError(t, err)
	require.Equal(t, "Docs", created.Name)
	require.Equal(t, "https://example.com/mcp", created.Connection.MCP.URL)
	require.Nil(t, created.Connection.HTTP)
	read, err := get.Execute(ctx, owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialBearer, Token: "test-token"}, read.Credential)
	require.Equal(t, domain.MCPServerTypeStreamableHTTP, read.Connection.MCP.ServerType)
	require.Equal(t, domain.BusinessSystemTransportMCP, read.Transport)
	input.Name = "docs"
	_, err = create.Execute(ctx, owner, input)
	var fields *common.FieldError
	require.ErrorAs(t, err, &fields, "duplicate name")
	require.Equal(t, businesssystemaction.ValidationNameDuplicate, fields.Fields["name"])
	_, err = create.Execute(ctx, other, input)
	require.NoError(t, err, "same name in other workspace")
	_, err = get.Execute(ctx, other, created.ID)
	require.ErrorIs(t, err, businesssystemaction.ErrNotFound, "cross-workspace read")
	_, err = update.Execute(ctx, other, created.ID, input)
	require.ErrorIs(t, err, businesssystemaction.ErrNotFound, "cross-workspace update")
	require.ErrorIs(t, remove.Execute(ctx, other, created.ID), businesssystemaction.ErrNotFound, "cross-workspace delete")
	records, err := list.Execute(ctx, owner)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, created.ID, records[0].ID)
	input = mcpSystemInput("Updated", "http://localhost:8080/sse", domain.MCPServerTypeSSE)
	saved, err := update.Execute(ctx, owner, created.ID, input)
	require.NoError(t, err)
	require.Equal(t, input.Name, saved.Name)
	require.Equal(t, "http://localhost:8080/sse", saved.Connection.MCP.URL)
	require.Equal(t, domain.MCPServerTypeSSE, saved.Connection.MCP.ServerType)
	require.Equal(t, domain.BusinessSystemCredentialNone, saved.Credential.Kind)
	input.Name = "Another"
	second, err := create.Execute(ctx, owner, input)
	require.NoError(t, err)
	input.Name = "UPDATED"
	_, err = update.Execute(ctx, owner, second.ID, input)
	require.ErrorAs(t, err, &fields, "duplicate update")
	require.Equal(t, businesssystemaction.ValidationNameDuplicate, fields.Fields["name"])
	require.NoError(t, remove.Execute(ctx, owner, created.ID))
	_, err = get.Execute(ctx, owner, created.ID)
	require.ErrorIs(t, err, businesssystemaction.ErrNotFound, "read deleted")
	require.NoError(t, remove.Execute(ctx, owner, second.ID))
	records, err = list.Execute(ctx, owner)
	require.NoError(t, err)
	require.NotNil(t, records)
	require.Empty(t, records)
}

// mcpSystemInput 返回不认证的 MCP 业务系统输入。
func mcpSystemInput(name, address string, serverType domain.MCPServerType) businesssystemaction.Input {
	return businesssystemaction.Input{Name: name, Connection: businesssystemaction.ConnectionInput{
		Transport:  domain.BusinessSystemTransportMCP,
		Connection: domain.BusinessSystemConnection{MCP: &domain.MCPConnection{URL: address, ServerType: serverType}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
	}}
}

// mcpDiscoverFunc 为工具目录测试提供可控的远端响应。
type mcpDiscoverFunc func(context.Context, mcpintegration.Config) ([]domain.BusinessTool, error)

// Discover 返回测试指定的工具目录或错误。
func (f mcpDiscoverFunc) Discover(ctx context.Context, config mcpintegration.Config) ([]domain.BusinessTool, error) {
	return f(ctx, config)
}

// TestBusinessSystemToolsUpdates 验证保存写入探测目录、批量更新去重、整批替换、工具设置保留与清理、失败保留及旧批次拒绝。
func TestBusinessSystemToolsUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "工具测试", DisplayName: "维护人员", Email: servertest.UniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	identity := installed.Identity
	schema := []byte(`{"type":"object","properties":{"customerId":{"type":"string"},"query":{"type":"string"}}}`)
	tools := []domain.BusinessTool{{Name: "search", Description: "查找文档", InputSchema: schema}, {Name: "read", Description: "读取文档"}}
	var discoverError error
	client := mcpDiscoverFunc(func(context.Context, mcpintegration.Config) ([]domain.BusinessTool, error) {
		return tools, discoverError
	})
	tasks := servertest.NewTasks()
	connector := businesssystemaction.NewConnector(client, nil)
	worker := businesssystemaction.NewUpdateToolsAction(db, connector)
	probe := businesssystemaction.NewTestConnectionAction(connector)
	create := businesssystemaction.NewCreateBusinessSystemAction(db, probe)
	update := businesssystemaction.NewUpdateBusinessSystemAction(db, probe)
	refresh := businesssystemaction.NewRefreshToolsAction(db, businesssystemaction.NewToolsScheduler(tasks))
	get := businesssystemaction.NewGetBusinessSystemQuery(db)
	input := mcpSystemInput("Docs", "https://example.com/mcp", domain.MCPServerTypeStreamableHTTP)
	record, err := create.Execute(ctx, identity, input)
	require.NoError(t, err)
	require.False(t, record.ToolsUpdating, "create must save probed tools")
	require.NotNil(t, record.ToolsUpdatedAt)
	require.Len(t, record.Tools, 2)
	require.NoError(t, refresh.Execute(ctx, identity))
	first := businessToolsRefreshInput(t, ctx, db, tasks, record.ID)
	require.NoError(t, refresh.Execute(ctx, identity))
	require.Equal(t, first, businessToolsRefreshInput(t, ctx, db, tasks, record.ID), "refresh duplicated pending task")
	readOnly := true
	setting := businesssystemaction.NewUpdateToolSettingAction(db)
	_, err = setting.Execute(ctx, identity, record.ID, businesssystemaction.ToolSettingInput{ToolName: "search", Setting: domain.BusinessToolSetting{
		ReadOnly: &readOnly, ParameterBindings: map[string]domain.ContextValue{"customerId": domain.ContextValueCustomerUserID},
	}})
	require.NoError(t, err)
	_, err = setting.Execute(ctx, identity, record.ID, businesssystemaction.ToolSettingInput{ToolName: "read", Setting: domain.BusinessToolSetting{Disabled: true}})
	require.NoError(t, err)
	var fields *common.FieldError
	_, err = setting.Execute(ctx, identity, record.ID, businesssystemaction.ToolSettingInput{ToolName: "read", Setting: domain.BusinessToolSetting{
		ParameterBindings: map[string]domain.ContextValue{"customerId": domain.ContextValueCustomerUserID},
	}})
	require.ErrorAs(t, err, &fields, "binding to a missing parameter")
	require.Equal(t, businesssystemaction.ValidationToolBindingInvalid, fields.Fields["parameterBindings"])
	_, err = setting.Execute(ctx, identity, record.ID, businesssystemaction.ToolSettingInput{ToolName: "missing"})
	require.ErrorIs(t, err, businesssystemaction.ErrToolNotFound, "setting of a missing tool")
	// 保存写入新目录、结束进行中的更新，只保留仍存在的工具的设置，并移除已不在参数定义中的参数绑定。
	tools = []domain.BusinessTool{{Name: "search", Description: "查找文档", InputSchema: []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}
	input.Name = "Renamed"
	input.HeaderBindings = map[string]domain.ContextValue{"x-member": domain.ContextValueMemberEmail}
	record, err = update.Execute(ctx, identity, record.ID, input)
	require.NoError(t, err)
	require.False(t, record.ToolsUpdating, "save must write probed tools")
	require.Len(t, record.Tools, 1)
	require.Equal(t, domain.ContextValueMemberEmail, record.HeaderBindings["X-Member"])
	kept := record.ToolSettings["search"]
	require.Len(t, record.ToolSettings, 1, "save must keep settings of remaining tools")
	require.NotNil(t, kept.ReadOnly)
	require.True(t, *kept.ReadOnly)
	require.Empty(t, kept.ParameterBindings)
	// 保存前投递的旧批次拒绝写回。
	tools = []domain.BusinessTool{{Name: "stale", Description: "旧目录"}}
	require.NoError(t, worker.Execute(ctx, first))
	require.NoError(t, worker.FinalizeFailure(ctx, first, errors.New("old worker")))
	record, _ = get.Execute(ctx, identity, record.ID)
	require.Len(t, record.Tools, 1, "old batch changed tools")
	require.Equal(t, "search", record.Tools[0].Name)
	require.Empty(t, record.ToolsFailure)
	// 更换地址后清除工具设置。
	tools = []domain.BusinessTool{{Name: "search", Description: "查找文档"}}
	input.Connection.Connection.MCP.URL = "https://example.com/other-mcp"
	record, err = update.Execute(ctx, identity, record.ID, input)
	require.NoError(t, err)
	require.Empty(t, record.ToolSettings, "URL change must clear settings")
	tools = []domain.BusinessTool{}
	require.NoError(t, refresh.Execute(ctx, identity))
	require.NoError(t, worker.Execute(ctx, businessToolsRefreshInput(t, ctx, db, tasks, record.ID)))
	record, _ = get.Execute(ctx, identity, record.ID)
	require.False(t, record.ToolsUpdating, "empty list must replace tools")
	require.NotNil(t, record.ToolsUpdatedAt)
	require.Empty(t, record.Tools)
	require.Empty(t, record.ToolSettings)
	// 保存探测失败时，配置和目录均保持原样。
	discoverError = connectiontest.NewError(connectiontest.StageAuthenticate, connectiontest.FailureUnauthorized, nil)
	input.Name = "Must not save"
	_, err = update.Execute(ctx, identity, record.ID, input)
	require.Error(t, err, "failed probe saved configuration")
	_, err = create.Execute(ctx, identity, input)
	require.Error(t, err, "failed probe created configuration")
	record, _ = get.Execute(ctx, identity, record.ID)
	require.Equal(t, "Renamed", record.Name, "failed save changed record")
	require.False(t, record.ToolsUpdating)
	require.NoError(t, refresh.Execute(ctx, identity))
	failed := businessToolsRefreshInput(t, ctx, db, tasks, record.ID)
	require.Error(t, worker.Execute(ctx, failed), "expected failed discovery")
	require.NoError(t, worker.FinalizeFailure(ctx, failed, discoverError))
	record, _ = get.Execute(ctx, identity, record.ID)
	require.False(t, record.ToolsUpdating, "failure did not preserve snapshot")
	require.Equal(t, string(connectiontest.FailureUnauthorized), record.ToolsFailure)
	require.NotNil(t, record.ToolsUpdatedAt)
	// 拒绝删除后的任务写回。
	discoverError = nil
	require.NoError(t, refresh.Execute(ctx, identity))
	pending := businessToolsRefreshInput(t, ctx, db, tasks, record.ID)
	require.NoError(t, businesssystemaction.NewDeleteBusinessSystemAction(db).Execute(ctx, identity, record.ID))
	require.NoError(t, worker.Execute(ctx, pending))
	require.NoError(t, worker.FinalizeFailure(ctx, pending, errors.New("deleted")))
}

// businessToolsRefreshInput 返回登记器中业务系统当前批次对应的唯一工具更新任务输入。
func businessToolsRefreshInput(t *testing.T, ctx context.Context, db *bun.DB, tasks *servertest.Tasks, businessSystemID string) businesssystemaction.RefreshToolsInput {
	t.Helper()
	var refreshID string
	require.NoError(t, db.NewSelect().TableExpr("business_systems").ColumnExpr("tools_refresh_id::text").Where("id = ?", businessSystemID).Scan(ctx, &refreshID))
	inputs := servertest.QueuedInputs(t, tasks, businesssystemaction.RefreshToolsActionName, func(input businesssystemaction.RefreshToolsInput) bool {
		return input.RefreshID == refreshID
	})
	require.Len(t, inputs, 1, "refresh tasks for batch %s", refreshID)
	return inputs[0]
}

// httpOrdersSpec 是 HTTP 业务系统测试使用的 OpenAPI 文档。
const httpOrdersSpec = `{"openapi":"3.0.3","servers":[{"url":"https://erp.example.com/api"}],"paths":{
"/customers/{customerId}/orders":{"get":{"operationId":"listOrders","summary":"客户订单","parameters":[
{"name":"customerId","in":"path","required":true,"schema":{"type":"string"}},{"name":"status","in":"query","schema":{"type":"string"}}]}},
"/orders/{id}/address":{"put":{"operationId":"updateAddress","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"address":{"type":"string"}}}}}}}}}}`

// TestHTTPBusinessSystem 验证 HTTP 业务系统按粘贴的文档生成工具、接口根地址取文档服务地址，文档无效时不保存，
// 修改接口根地址或文档正文保留工具设置，改为文档地址后清除工具设置，文档失效的目录更新按服务未返回可用目录记录。
func TestHTTPBusinessSystem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	identity := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "接口测试", DisplayName: "维护人员", Email: servertest.UniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	}).Identity
	var specBroken atomic.Bool
	specServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if specBroken.Load() {
			_, _ = w.Write([]byte("<html></html>"))
			return
		}
		_, _ = w.Write([]byte(httpOrdersSpec))
	}))
	defer specServer.Close()
	connector := businesssystemaction.NewConnector(nil, openapi.NewClient())
	probe := businesssystemaction.NewTestConnectionAction(connector)
	create := businesssystemaction.NewCreateBusinessSystemAction(db, probe)
	update := businesssystemaction.NewUpdateBusinessSystemAction(db, probe)
	setting := businesssystemaction.NewUpdateToolSettingAction(db)
	input := businesssystemaction.Input{Name: "ERP", Connection: businesssystemaction.ConnectionInput{
		Transport:  domain.BusinessSystemTransportHTTP,
		Connection: domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{Spec: "{\"openapi\":\"2.0\"}"}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialHeader, HeaderName: "x-api-key", Token: "k"},
	}}
	var fields *common.FieldError
	_, err = create.Execute(ctx, identity, input)
	require.ErrorAs(t, err, &fields, "invalid spec")
	require.Equal(t, businesssystemaction.ValidationSpecInvalid, fields.Fields["spec"])
	input.Connection.Connection.HTTP.Spec = httpOrdersSpec
	record, err := create.Execute(ctx, identity, input)
	require.NoError(t, err)
	require.Equal(t, domain.BusinessSystemTransportHTTP, record.Transport, "created = %+v", record)
	require.Equal(t, "https://erp.example.com/api", record.Connection.HTTP.BaseURL)
	require.Len(t, record.Tools, 2)
	require.Equal(t, "listOrders", record.Tools[0].Name)
	require.Equal(t, "GET", record.Tools[0].HTTP.Method)
	require.Equal(t, "X-Api-Key", record.Credential.HeaderName)
	facts := domain.DefaultToolFacts(record.Tools[1])
	require.False(t, facts.ReadOnly, "PUT default facts = %+v", facts)
	require.False(t, facts.Reversible, "PUT default facts = %+v", facts)
	_, err = setting.Execute(ctx, identity, record.ID, businesssystemaction.ToolSettingInput{ToolName: "listOrders", Setting: domain.BusinessToolSetting{
		ParameterBindings: map[string]domain.ContextValue{"customerId": domain.ContextValueCustomerUserID},
	}})
	require.NoError(t, err)
	_, err = update.Execute(ctx, identity, record.ID, mcpSystemInput("ERP", "https://erp.example.com/mcp", domain.MCPServerTypeSSE))
	require.ErrorAs(t, err, &fields, "transport change")
	require.Equal(t, businesssystemaction.ValidationTransportChanged, fields.Fields["transport"])
	input.Connection.Connection.HTTP.BaseURL = "https://staging.example.com/api/"
	record, err = update.Execute(ctx, identity, record.ID, input)
	require.NoError(t, err)
	require.Equal(t, "https://staging.example.com/api", record.Connection.HTTP.BaseURL, "base url change")
	require.Len(t, record.ToolSettings["listOrders"].ParameterBindings, 1)
	input.Connection.Connection.HTTP.SpecURL = specServer.URL + "/openapi.json"
	record, err = update.Execute(ctx, identity, record.ID, input)
	require.NoError(t, err)
	require.Empty(t, record.Connection.HTTP.Spec, "spec source change")
	require.Empty(t, record.ToolSettings)
	require.Len(t, record.Tools, 2)
	// 文档地址返回的内容失效后，目录更新保留已有目录并记录服务未返回可用目录。
	specBroken.Store(true)
	tasks := servertest.NewTasks()
	worker := businesssystemaction.NewUpdateToolsAction(db, connector)
	require.NoError(t, businesssystemaction.NewRefreshToolsAction(db, businesssystemaction.NewToolsScheduler(tasks)).Execute(ctx, identity))
	pending := businessToolsRefreshInput(t, ctx, db, tasks, record.ID)
	runErr := worker.Execute(ctx, pending)
	_, kind, _ := connectiontest.Details(runErr)
	require.Equal(t, connectiontest.FailureProtocol, kind, "invalid spec refresh error = %v", runErr)
	require.NoError(t, worker.FinalizeFailure(ctx, pending, runErr))
	read, _ := businesssystemaction.NewGetBusinessSystemQuery(db).Execute(ctx, identity, record.ID)
	require.Equal(t, string(connectiontest.FailureProtocol), read.ToolsFailure, "failed refresh")
	require.Len(t, read.Tools, 2)
}
