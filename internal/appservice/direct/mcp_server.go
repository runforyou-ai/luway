//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	aimodelaction "github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	mcpserveraction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// integrationOps 持有模型服务与 MCP 的 Action 和 Query。
type integrationOps struct {
	listAIModelOptions       *aimodelaction.ListOptionsQuery
	listAIProviders          *aiprovideraction.ListAIProvidersQuery
	getAIProvider            *aiprovideraction.GetAIProviderQuery
	testAIProviderConnection *aiprovideraction.TestConnectionAction
	discoverAIProviderModels *aiprovideraction.DiscoverModelsAction
	createAIProvider         *aiprovideraction.CreateAIProviderAction
	updateAIProvider         *aiprovideraction.UpdateAIProviderAction
	deleteAIProvider         *aiprovideraction.DeleteAIProviderAction
	listMCPServers           *mcpserveraction.ListMCPServersQuery
	getMCPServer             *mcpserveraction.GetMCPServerQuery
	createMCPServer          *mcpserveraction.CreateMCPServerAction
	updateMCPServer          *mcpserveraction.UpdateMCPServerAction
	updateMCPToolPurpose     *mcpserveraction.UpdateToolPurposeAction
	deleteMCPServer          *mcpserveraction.DeleteMCPServerAction
	testMCPServerConnection  *mcpserveraction.TestConnectionAction
	refreshMCPServerTools    *mcpserveraction.RefreshToolsAction
}

// newIntegrationOps 创建模型服务与 MCP 的业务实现依赖。
func newIntegrationOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, connectionRunner *connectiontest.Runner, modelProviderRegistry *modelprovider.Registry, mcpTest *mcpserveraction.TestConnectionAction, mcpScheduler *mcpserveraction.ToolsScheduler) integrationOps {
	return integrationOps{
		listAIModelOptions:       aimodelaction.NewListOptionsQuery(db),
		listAIProviders:          aiprovideraction.NewListAIProvidersQuery(db),
		getAIProvider:            aiprovideraction.NewGetAIProviderQuery(db),
		testAIProviderConnection: aiprovideraction.NewTestConnectionAction(connectionRunner, modelProviderRegistry),
		discoverAIProviderModels: aiprovideraction.NewDiscoverModelsAction(modelProviderRegistry),
		createAIProvider:         aiprovideraction.NewCreateAIProviderAction(db),
		updateAIProvider:         aiprovideraction.NewUpdateAIProviderAction(db, knowledgebaseaction.NewEmbeddingReindexer(db, taskEnqueuer)),
		deleteAIProvider:         aiprovideraction.NewDeleteAIProviderAction(db),
		listMCPServers:           mcpserveraction.NewListMCPServersQuery(db),
		getMCPServer:             mcpserveraction.NewGetMCPServerQuery(db),
		createMCPServer:          mcpserveraction.NewCreateMCPServerAction(db, mcpTest),
		updateMCPServer:          mcpserveraction.NewUpdateMCPServerAction(db, mcpTest),
		updateMCPToolPurpose:     mcpserveraction.NewUpdateToolPurposeAction(db),
		deleteMCPServer:          mcpserveraction.NewDeleteMCPServerAction(db),
		testMCPServerConnection:  mcpTest,
		refreshMCPServerTools:    mcpserveraction.NewRefreshToolsAction(db, mcpScheduler),
	}
}

// ListMCPServers 返回当前企业配置的 MCP 服务。
func (o *directOperations) ListMCPServers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.MCPServerList, error) {
	records, err := o.listMCPServers.Execute(ctx, identity)
	if err != nil {
		return appservice.MCPServerList{}, o.mcpServerError(meta, err, i18n.ErrorMCPServerListFailed)
	}
	mcpServers := make([]appservice.MCPServer, 0, len(records))
	for _, record := range records {
		mcpServers = append(mcpServers, mcpServerFromAction(meta, record))
	}
	return appservice.MCPServerList{MCPServers: mcpServers}, nil
}

// GetMCPServer 返回当前企业中的 MCP 服务详情。
func (o *directOperations) GetMCPServer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, mcpServerID string) (appservice.MCPServer, error) {
	record, err := o.getMCPServer.Execute(ctx, identity, mcpServerID)
	if err != nil {
		return appservice.MCPServer{}, o.mcpServerError(meta, err, i18n.ErrorMCPServerReadFailed)
	}
	return mcpServerFromAction(meta, *record), nil
}

// CreateMCPServer 创建 MCP 服务。
func (o *directOperations) CreateMCPServer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.MCPServerInput) (appservice.MCPServer, error) {
	record, err := o.createMCPServer.Execute(ctx, identity, mcpServerInput(input))
	if err != nil {
		return appservice.MCPServer{}, o.mcpServerMutationError(meta, err, i18n.ErrorMCPServerCreateFailed)
	}
	slog.Info(
		"MCP 服务创建成功",
		"organization_id", identity.Organization.ID,
		"mcp_server_id", record.ID,
		"server_type", record.ServerType,
	)
	return mcpServerFromAction(meta, *record), nil
}

// UpdateMCPServer 修改 MCP 服务。
func (o *directOperations) UpdateMCPServer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, mcpServerID string, input appservice.MCPServerInput) (appservice.MCPServer, error) {
	record, err := o.updateMCPServer.Execute(ctx, identity, mcpServerID, mcpServerInput(input))
	if err != nil {
		return appservice.MCPServer{}, o.mcpServerMutationError(meta, err, i18n.ErrorMCPServerUpdateFailed)
	}
	slog.Info(
		"MCP 服务保存成功",
		"organization_id", identity.Organization.ID,
		"mcp_server_id", record.ID,
		"server_type", record.ServerType,
	)
	return mcpServerFromAction(meta, *record), nil
}

// UpdateMCPToolPurpose 标记 MCP 服务中一个工具的用途。
func (o *directOperations) UpdateMCPToolPurpose(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, mcpServerID string, input appservice.MCPToolPurposeInput) (appservice.MCPServer, error) {
	record, err := o.updateMCPToolPurpose.Execute(ctx, identity, mcpServerID, mcpserveraction.ToolPurposeInput{
		ToolName: input.ToolName, Purpose: domain.MCPToolPurpose(input.Purpose),
	})
	if errors.Is(err, mcpserveraction.ErrToolNotFound) {
		return appservice.MCPServer{}, appservice.NotFoundError(meta, i18n.ErrorMCPToolNotFound)
	}
	if err != nil {
		return appservice.MCPServer{}, o.mcpServerMutationError(meta, err, i18n.ErrorMCPServerUpdateFailed)
	}
	return mcpServerFromAction(meta, *record), nil
}

// DeleteMCPServer 删除 MCP 服务。
func (o *directOperations) DeleteMCPServer(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, mcpServerID string) error {
	if err := o.deleteMCPServer.Execute(ctx, identity, mcpServerID); err != nil {
		return o.mcpServerError(meta, err, i18n.ErrorMCPServerDeleteFailed)
	}
	return nil
}

// mcpServerMutationError 转换 MCP 服务写入错误。
func (o *directOperations) mcpServerMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 映射 MCP 服务校验错误。
		keys := map[common.FieldCode]i18n.Key{
			mcpserveraction.ValidationServerTypeInvalid:  i18n.FieldMCPServerTypeInvalid,
			mcpserveraction.ValidationNameRequired:       i18n.FieldMCPServerNameRequired,
			mcpserveraction.ValidationNameTooLong:        i18n.FieldMCPServerNameTooLong,
			mcpserveraction.ValidationNameDuplicate:      i18n.FieldMCPServerNameDuplicate,
			mcpserveraction.ValidationURLRequired:        i18n.FieldMCPServerURLRequired,
			mcpserveraction.ValidationURLInvalid:         i18n.FieldHTTPURLInvalid,
			mcpserveraction.ValidationURLTooLong:         i18n.FieldMCPServerURLTooLong,
			mcpserveraction.ValidationToolPurposeInvalid: i18n.FieldMCPToolPurposeInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	return o.mcpServerError(meta, err, failureKey)
}

// mcpServerError 转换 MCP 服务操作错误。
func (o *directOperations) mcpServerError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, mcpserveraction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorMCPServerNotFound)
	}
	if _, kind, ok := connectiontest.Details(err); ok {
		return appservice.UnavailableError(meta, mcpConnectionFailureKey(kind), nil)
	}
	return appservice.FailedError(meta, failureKey, err)
}

// mcpServerInput 转换 MCP 服务输入。
func mcpServerInput(input appservice.MCPServerInput) mcpserveraction.Input {
	return mcpserveraction.Input{
		Name: input.Name, URL: input.URL, ServerType: domain.MCPServerType(input.ServerType), AuthorizationToken: input.AuthorizationToken,
		CustomerScoped: input.CustomerScoped,
	}
}

// mcpServerFromAction 转换 MCP 服务输出。
func mcpServerFromAction(meta appservice.RequestMeta, input mcpserveraction.Record) appservice.MCPServer {
	tools := make([]appservice.MCPTool, 0, len(input.Tools))
	for _, item := range input.Tools {
		tools = append(tools, appservice.MCPTool{Name: item.Name, Description: item.Description, Purpose: appservice.MCPToolPurpose(input.ToolPurposes[item.Name])})
	}
	message := ""
	if input.ToolsFailure != "" {
		message, _ = i18n.Localize(string(meta.Locale), mcpConnectionFailureKey(connectiontest.FailureKind(input.ToolsFailure)))
	}
	return appservice.MCPServer{
		Tools: tools, ToolsUpdatedAt: input.ToolsUpdatedAt, ToolsUpdating: input.ToolsUpdating, ToolsError: message,
		ID: input.ID, Name: input.Name, URL: input.URL, ServerType: appservice.MCPServerType(input.ServerType), AuthorizationToken: input.AuthorizationToken,
		CustomerScoped: input.CustomerScoped, CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}

// TestMCPServerConnection 测试未保存的 MCP 连接配置。
func (o *directOperations) TestMCPServerConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.MCPServerConnectionInput) error {
	_, err := o.testMCPServerConnection.Execute(ctx, mcpserveraction.ConnectionInput{URL: input.URL, ServerType: domain.MCPServerType(input.ServerType), AuthorizationToken: input.AuthorizationToken})
	if err == nil {
		return nil
	}
	return o.mcpServerMutationError(meta, err, i18n.ErrorMCPConnectionFailed)
}

// TestSavedMCPServerConnection 测试当前企业中已保存的 MCP 服务。
func (o *directOperations) TestSavedMCPServerConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, mcpServerID string) error {
	record, err := o.getMCPServer.Execute(ctx, identity, mcpServerID)
	if err == nil {
		_, err = o.testMCPServerConnection.Execute(ctx, mcpserveraction.ConnectionInput{URL: record.URL, ServerType: record.ServerType, AuthorizationToken: record.AuthorizationToken})
	}
	if err == nil {
		return nil
	}
	return o.mcpServerMutationError(meta, err, i18n.ErrorMCPConnectionFailed)
}

// RefreshMCPServerTools 提交当前企业全部 MCP 服务的工具更新任务。
func (o *directOperations) RefreshMCPServerTools(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) error {
	if err := o.refreshMCPServerTools.Execute(ctx, identity); err != nil {
		return o.mcpServerError(meta, err, i18n.ErrorMCPToolsRefreshFailed)
	}
	return nil
}

// mcpConnectionFailureKey 将 MCP 连接失败原因映射为用户文案。
func mcpConnectionFailureKey(kind connectiontest.FailureKind) i18n.Key {
	switch kind {
	case connectiontest.FailureUnauthorized:
		return i18n.ErrorMCPAuthenticationFailed
	case connectiontest.FailureForbidden:
		return i18n.ErrorMCPAuthorizationFailed
	case connectiontest.FailureTimeout:
		return i18n.ErrorMCPConnectionTimeout
	case connectiontest.FailureProtocol:
		return i18n.ErrorMCPProtocolFailed
	default:
		return i18n.ErrorMCPConnectionFailed
	}
}
