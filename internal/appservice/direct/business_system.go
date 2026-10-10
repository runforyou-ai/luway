//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	aimodelaction "github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	businesssystemaction "github.com/runforyou-ai/luway/internal/actions/businesssystem"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/mapx"
	"github.com/uptrace/bun"
)

// integrationOps 持有模型服务与业务系统的 Action 和 Query。
type integrationOps struct {
	listAIModelOptions           *aimodelaction.ListOptionsQuery
	listAIProviders              *aiprovideraction.ListAIProvidersQuery
	getAIProvider                *aiprovideraction.GetAIProviderQuery
	testAIProviderConnection     *aiprovideraction.TestConnectionAction
	discoverAIProviderModels     *aiprovideraction.DiscoverModelsAction
	createAIProvider             *aiprovideraction.CreateAIProviderAction
	updateAIProvider             *aiprovideraction.UpdateAIProviderAction
	deleteAIProvider             *aiprovideraction.DeleteAIProviderAction
	listBusinessSystems          *businesssystemaction.ListBusinessSystemsQuery
	getBusinessSystem            *businesssystemaction.GetBusinessSystemQuery
	createBusinessSystem         *businesssystemaction.CreateBusinessSystemAction
	updateBusinessSystem         *businesssystemaction.UpdateBusinessSystemAction
	updateBusinessToolSetting    *businesssystemaction.UpdateToolSettingAction
	deleteBusinessSystem         *businesssystemaction.DeleteBusinessSystemAction
	testBusinessSystemConnection *businesssystemaction.TestConnectionAction
	refreshBusinessSystemTools   *businesssystemaction.RefreshToolsAction
}

// newIntegrationOps 创建模型服务与业务系统的业务实现依赖。
func newIntegrationOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, connectionRunner *connectiontest.Runner, connectionClient connectiontest.HTTPDoer, businessSystemTest *businesssystemaction.TestConnectionAction, toolsScheduler *businesssystemaction.ToolsScheduler) *integrationOps {
	return &integrationOps{
		listAIModelOptions:           aimodelaction.NewListOptionsQuery(db),
		listAIProviders:              aiprovideraction.NewListAIProvidersQuery(db),
		getAIProvider:                aiprovideraction.NewGetAIProviderQuery(db),
		testAIProviderConnection:     aiprovideraction.NewTestConnectionAction(connectionRunner, connectionClient),
		discoverAIProviderModels:     aiprovideraction.NewDiscoverModelsAction(connectionClient),
		createAIProvider:             aiprovideraction.NewCreateAIProviderAction(db),
		updateAIProvider:             aiprovideraction.NewUpdateAIProviderAction(db, knowledgebaseaction.NewEmbeddingReindexer(db, taskEnqueuer)),
		deleteAIProvider:             aiprovideraction.NewDeleteAIProviderAction(db),
		listBusinessSystems:          businesssystemaction.NewListBusinessSystemsQuery(db),
		getBusinessSystem:            businesssystemaction.NewGetBusinessSystemQuery(db),
		createBusinessSystem:         businesssystemaction.NewCreateBusinessSystemAction(db, businessSystemTest),
		updateBusinessSystem:         businesssystemaction.NewUpdateBusinessSystemAction(db, businessSystemTest),
		updateBusinessToolSetting:    businesssystemaction.NewUpdateToolSettingAction(db),
		deleteBusinessSystem:         businesssystemaction.NewDeleteBusinessSystemAction(db),
		testBusinessSystemConnection: businessSystemTest,
		refreshBusinessSystemTools:   businesssystemaction.NewRefreshToolsAction(db, toolsScheduler),
	}
}

// ListBusinessSystems 返回当前工作区配置的业务系统。
func (o *integrationOps) ListBusinessSystems(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.BusinessSystemList, error) {
	records, err := o.listBusinessSystems.Execute(ctx, identity)
	if err != nil {
		return appservice.BusinessSystemList{}, businessSystemError(meta, err, i18n.ErrorBusinessSystemListFailed)
	}
	systems := make([]appservice.BusinessSystem, 0, len(records))
	for _, record := range records {
		system := businessSystemFromAction(meta, record)
		// 列表不返回粘贴的文档正文，编辑页读取详情时取得正文。
		if system.HTTP != nil {
			system.HTTP.Spec = ""
		}
		systems = append(systems, system)
	}
	return appservice.BusinessSystemList{BusinessSystems: systems}, nil
}

// GetBusinessSystem 返回当前工作区中的业务系统详情。
func (o *integrationOps) GetBusinessSystem(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, businessSystemID string) (appservice.BusinessSystem, error) {
	record, err := o.getBusinessSystem.Execute(ctx, identity, businessSystemID)
	if err != nil {
		return appservice.BusinessSystem{}, businessSystemError(meta, err, i18n.ErrorBusinessSystemReadFailed)
	}
	return businessSystemFromAction(meta, *record), nil
}

// CreateBusinessSystem 创建业务系统。
func (o *integrationOps) CreateBusinessSystem(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.BusinessSystemInput) (appservice.BusinessSystem, error) {
	record, err := o.createBusinessSystem.Execute(ctx, identity, businessSystemInput(input))
	if err != nil {
		return appservice.BusinessSystem{}, businessSystemMutationError(meta, err, i18n.ErrorBusinessSystemCreateFailed)
	}
	slog.InfoContext(ctx, "业务系统创建成功", "business_system_id", record.ID, "transport", record.Transport)
	return businessSystemFromAction(meta, *record), nil
}

// UpdateBusinessSystem 修改业务系统。
func (o *integrationOps) UpdateBusinessSystem(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, businessSystemID string, input appservice.BusinessSystemInput) (appservice.BusinessSystem, error) {
	record, err := o.updateBusinessSystem.Execute(ctx, identity, businessSystemID, businessSystemInput(input))
	if err != nil {
		return appservice.BusinessSystem{}, businessSystemMutationError(meta, err, i18n.ErrorBusinessSystemUpdateFailed)
	}
	slog.InfoContext(ctx, "业务系统保存成功", "business_system_id", record.ID, "transport", record.Transport)
	return businessSystemFromAction(meta, *record), nil
}

// UpdateBusinessToolSetting 保存业务系统中一个工具的事实修正、停用与参数绑定。
func (o *integrationOps) UpdateBusinessToolSetting(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, businessSystemID string, input appservice.BusinessToolSettingInput) (appservice.BusinessSystem, error) {
	setting := domain.BusinessToolSetting{ReadOnly: input.ReadOnly, Reversible: input.Reversible, Outbound: input.Outbound, Disabled: input.Disabled}
	if len(input.ParameterBindings) > 0 {
		setting.ParameterBindings = arr.Associate(input.ParameterBindings, func(binding appservice.ParameterBinding) (string, domain.ContextValue) {
			return binding.Parameter, binding.Value
		})
	}
	record, err := o.updateBusinessToolSetting.Execute(ctx, identity, businessSystemID, businesssystemaction.ToolSettingInput{ToolName: input.ToolName, Setting: setting})
	if errors.Is(err, businesssystemaction.ErrToolNotFound) {
		return appservice.BusinessSystem{}, appservice.NotFoundError(meta, i18n.ErrorBusinessToolNotFound)
	}
	if err != nil {
		return appservice.BusinessSystem{}, businessSystemMutationError(meta, err, i18n.ErrorBusinessSystemUpdateFailed)
	}
	return businessSystemFromAction(meta, *record), nil
}

// DeleteBusinessSystem 删除业务系统。
func (o *integrationOps) DeleteBusinessSystem(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, businessSystemID string) error {
	if err := o.deleteBusinessSystem.Execute(ctx, identity, businessSystemID); err != nil {
		return businessSystemError(meta, err, i18n.ErrorBusinessSystemDeleteFailed)
	}
	return nil
}

// businessSystemFieldKeys 把业务系统校验错误码映射为本地化文案键。
var businessSystemFieldKeys = map[common.FieldCode]i18n.Key{
	businesssystemaction.ValidationTransportChanged:        i18n.FieldBusinessSystemTransportChanged,
	businesssystemaction.ValidationTransportInvalid:        i18n.FieldBusinessSystemTransportInvalid,
	businesssystemaction.ValidationServerTypeInvalid:       i18n.FieldBusinessSystemServerTypeInvalid,
	businesssystemaction.ValidationNameDuplicate:           i18n.FieldBusinessSystemNameDuplicate,
	businesssystemaction.ValidationURLRequired:             i18n.FieldBusinessSystemURLRequired,
	businesssystemaction.ValidationURLInvalid:              i18n.FieldHTTPURLInvalid,
	businesssystemaction.ValidationURLTooLong:              i18n.FieldBusinessSystemURLTooLong,
	businesssystemaction.ValidationSpecRequired:            i18n.FieldBusinessSystemSpecRequired,
	businesssystemaction.ValidationSpecInvalid:             i18n.FieldBusinessSystemSpecInvalid,
	businesssystemaction.ValidationSpecEmpty:               i18n.FieldBusinessSystemSpecEmpty,
	businesssystemaction.ValidationBaseURLRequired:         i18n.FieldBusinessSystemBaseURLRequired,
	businesssystemaction.ValidationCredentialInvalid:       i18n.FieldBusinessSystemCredentialInvalid,
	businesssystemaction.ValidationCredentialHeaderInvalid: i18n.FieldBusinessSystemCredentialHeaderInvalid,
	businesssystemaction.ValidationCredentialRequired:      i18n.FieldBusinessSystemCredentialRequired,
	businesssystemaction.ValidationHeaderBindingInvalid:    i18n.FieldBusinessSystemHeaderBindingInvalid,
	businesssystemaction.ValidationToolBindingInvalid:      i18n.FieldBusinessToolBindingInvalid,
}

// businessSystemMutationError 转换业务系统写入错误。
func businessSystemMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return businessSystemMutationErrors.Translate(meta, err, failureKey)
}

// businessSystemErrors 是业务系统操作的错误转换规则，已分类的连接失败按失败原因提示。
var businessSystemErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(businesssystemaction.ErrNotFound, dispatch.NotFound(i18n.ErrorBusinessSystemNotFound)),
	dispatch.Is(businesssystemaction.ErrConnectTimeout, dispatch.Unavailable(i18n.ErrorBusinessSystemConnectionTimeout)),
	func(meta appservice.RequestMeta, err error) error {
		if _, kind, ok := connectiontest.Details(err); ok {
			return appservice.UnavailableError(meta, businessSystemConnectionFailureKey(kind), nil)
		}
		return nil
	},
})

// businessSystemMutationErrors 是业务系统写入的错误转换规则。
var businessSystemMutationErrors = dispatch.Catalogs(dispatch.Catalog{dispatch.FieldRule(businessSystemFieldKeys)}, businessSystemErrors)

// businessSystemError 转换业务系统操作错误。
func businessSystemError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return businessSystemErrors.Translate(meta, err, failureKey)
}

// businessSystemInput 转换业务系统输入，同名请求头以后出现的绑定为准。
func businessSystemInput(input appservice.BusinessSystemInput) businesssystemaction.Input {
	bindings := arr.Associate(input.HeaderBindings, func(binding appservice.HeaderBinding) (string, domain.ContextValue) {
		return binding.Header, binding.Value
	})
	return businesssystemaction.Input{
		Name: input.Name, HeaderBindings: bindings,
		Connection: businessSystemConnectionInput(appservice.BusinessSystemConnectionInput{Transport: input.Transport, MCP: input.MCP, HTTP: input.HTTP, Credential: input.Credential}),
	}
}

// businessSystemConnectionInput 转换业务系统连接配置与凭据。
func businessSystemConnectionInput(input appservice.BusinessSystemConnectionInput) businesssystemaction.ConnectionInput {
	connection := domain.BusinessSystemConnection{}
	if input.MCP != nil {
		connection.MCP = &domain.MCPConnection{URL: input.MCP.URL, ServerType: input.MCP.ServerType}
	}
	if input.HTTP != nil {
		connection.HTTP = &domain.HTTPConnection{BaseURL: input.HTTP.BaseURL, SpecURL: input.HTTP.SpecURL, Spec: input.HTTP.Spec}
	}
	credential := input.Credential
	return businesssystemaction.ConnectionInput{
		Transport: input.Transport, Connection: connection,
		Credential: domain.BusinessSystemCredential{
			Kind: credential.Kind, HeaderName: credential.HeaderName,
			Token: credential.Token, Username: credential.Username, Password: credential.Password,
		},
	}
}

// businessSystemFromAction 转换业务系统输出：绑定按名称排序，工具给出默认事实、管理员设置、生效事实与操作级别。
func businessSystemFromAction(meta appservice.RequestMeta, input businesssystemaction.Record) appservice.BusinessSystem {
	headers := arr.Map(mapx.SortedEntries(input.HeaderBindings), func(entry mapx.Entry[string, domain.ContextValue]) appservice.HeaderBinding {
		return appservice.HeaderBinding{Header: entry.Key, Value: entry.Value}
	})
	tools := make([]appservice.BusinessTool, 0, len(input.Tools))
	for _, item := range input.Tools {
		setting := input.ToolSettings[item.Name]
		parameters := arr.Map(mapx.SortedEntries(setting.ParameterBindings), func(entry mapx.Entry[string, domain.ContextValue]) appservice.ParameterBinding {
			return appservice.ParameterBinding{Parameter: entry.Key, Value: entry.Value}
		})
		facts := domain.EffectiveToolFacts(item, setting)
		tool := appservice.BusinessTool{
			Name: item.Name, Description: item.Description, Parameters: item.Parameters(),
			DefaultFacts: toolFacts(domain.DefaultToolFacts(item)),
			ReadOnly:     setting.ReadOnly, Reversible: setting.Reversible, Outbound: setting.Outbound,
			Disabled: setting.Disabled, ParameterBindings: parameters, Facts: toolFacts(facts),
			Level: domain.ToolLevel(facts, domain.IdentityBound(input.HeaderBindings, setting)),
		}
		if item.HTTP != nil {
			tool.HTTP = &appservice.BusinessToolHTTP{Method: item.HTTP.Method, Path: item.HTTP.Path}
		}
		tools = append(tools, tool)
	}
	message := ""
	if input.ToolsFailure != "" {
		message, _ = i18n.Localize(string(meta.Locale), businessSystemConnectionFailureKey(connectiontest.FailureKind(input.ToolsFailure)))
	}
	output := appservice.BusinessSystem{
		ID: input.ID, Name: input.Name, Transport: input.Transport,
		Credential: appservice.BusinessSystemCredential{
			Kind: input.Credential.Kind, HeaderName: input.Credential.HeaderName,
			Token: input.Credential.Token, Username: input.Credential.Username, Password: input.Credential.Password,
		},
		HeaderBindings: headers, Tools: tools, ToolsUpdatedAt: input.ToolsUpdatedAt, ToolsUpdating: input.ToolsUpdating, ToolsError: message,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
	if connection := input.Connection.MCP; connection != nil {
		output.MCP = &appservice.BusinessSystemMCPConnection{URL: connection.URL, ServerType: connection.ServerType}
	}
	if connection := input.Connection.HTTP; connection != nil {
		output.HTTP = &appservice.BusinessSystemHTTPConnection{BaseURL: connection.BaseURL, SpecURL: connection.SpecURL, Spec: connection.Spec}
	}
	return output
}

// toolFacts 转换工具事实。
func toolFacts(facts domain.ToolFacts) appservice.ToolFacts {
	return appservice.ToolFacts{ReadOnly: facts.ReadOnly, Reversible: facts.Reversible, Outbound: facts.Outbound}
}

// TestBusinessSystemConnection 测试未保存的业务系统连接配置。
func (o *integrationOps) TestBusinessSystemConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.BusinessSystemConnectionInput) error {
	_, _, err := o.testBusinessSystemConnection.Execute(ctx, businessSystemConnectionInput(input))
	if err == nil {
		return nil
	}
	return businessSystemMutationError(meta, err, i18n.ErrorBusinessSystemConnectionFailed)
}

// TestSavedBusinessSystemConnection 测试当前工作区中已保存的业务系统。
func (o *integrationOps) TestSavedBusinessSystemConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, businessSystemID string) error {
	record, err := o.getBusinessSystem.Execute(ctx, identity, businessSystemID)
	if err == nil {
		_, _, err = o.testBusinessSystemConnection.Execute(ctx, businesssystemaction.ConnectionInput{Transport: record.Transport, Connection: record.Connection, Credential: record.Credential})
	}
	if err == nil {
		return nil
	}
	return businessSystemMutationError(meta, err, i18n.ErrorBusinessSystemConnectionFailed)
}

// RefreshBusinessSystemTools 提交当前工作区全部业务系统的工具目录更新任务。
func (o *integrationOps) RefreshBusinessSystemTools(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) error {
	if err := o.refreshBusinessSystemTools.Execute(ctx, identity); err != nil {
		return businessSystemError(meta, err, i18n.ErrorBusinessSystemToolsRefreshFailed)
	}
	return nil
}

// businessSystemConnectionFailureKey 将业务系统连接失败原因映射为用户文案。
func businessSystemConnectionFailureKey(kind connectiontest.FailureKind) i18n.Key {
	switch kind {
	case connectiontest.FailureUnauthorized:
		return i18n.ErrorBusinessSystemAuthenticationFailed
	case connectiontest.FailureForbidden:
		return i18n.ErrorBusinessSystemAuthorizationFailed
	case connectiontest.FailureTimeout:
		return i18n.ErrorBusinessSystemConnectionTimeout
	case connectiontest.FailureProtocol:
		return i18n.ErrorBusinessSystemProtocolFailed
	default:
		return i18n.ErrorBusinessSystemConnectionFailed
	}
}
