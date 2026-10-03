//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// agentOps 持有 AI 员工与运行的 Action 和 Query。
type agentOps struct {
	agentCoordinator          *agentrunaction.ExecuteAction
	serviceReplySuggestions   *agentrunaction.GenerateServiceReplySuggestionsAction
	listServiceReplyAgents    *agentrunaction.ListServiceReplyAgentsQuery
	listAgentMCPServerOptions *agentaction.ListMCPServerOptionsQuery
	createAgent               *agentaction.CreateAgentAction
	listAgents                *agentaction.ListAgentsQuery
	getAgent                  *agentaction.GetAgentQuery
	updateAgent               *agentaction.UpdateAgentAction
	updateAgentExecution      *agentaction.UpdateExecutionAction
	updateAgentStatus         *agentaction.UpdateStatusAction
}

// newAgentOps 创建 AI 员工与运行的业务实现依赖。
func newAgentOps(db *bun.DB, agentCoordinator *agentrunaction.ExecuteAction, serviceReplySuggestions *agentrunaction.GenerateServiceReplySuggestionsAction) agentOps {
	return agentOps{
		agentCoordinator:          agentCoordinator,
		serviceReplySuggestions:   serviceReplySuggestions,
		listServiceReplyAgents:    agentrunaction.NewListServiceReplyAgentsQuery(db),
		listAgentMCPServerOptions: agentaction.NewListMCPServerOptionsQuery(db),
		createAgent:               agentaction.NewCreateAgentAction(db),
		listAgents:                agentaction.NewListAgentsQuery(db),
		getAgent:                  agentaction.NewGetAgentQuery(db),
		updateAgent:               agentaction.NewUpdateAgentAction(db, agentCoordinator),
		updateAgentExecution:      agentaction.NewUpdateExecutionAction(db),
		updateAgentStatus:         agentaction.NewUpdateStatusAction(db, agentCoordinator),
	}
}

// CreateAgent 创建企业 AI 员工。
func (o *directOperations) CreateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreateAgentInput) (appservice.Agent, error) {
	created, err := o.createAgent.Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: input.DisplayName, TeamIDs: input.TeamIDs,
		ServiceAudiences: serviceAudiencesInput(input.ServiceAudiences), AvatarFileID: input.AvatarFileID,
		Execution: agentExecutionInput(input.Execution),
	})
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, i18n.ErrorAgentCreateFailed, identity.Organization.ID, "", map[common.FieldCode]i18n.Key{
			agentaction.ValidationDisplayNameRequired:      i18n.FieldAgentNameRequired,
			agentaction.ValidationDisplayNameInvalid:       i18n.FieldDisplayNameInvalid,
			agentaction.ValidationTeamInvalid:              i18n.FieldTeamInvalid,
			agentaction.ValidationServiceAudienceInvalid:   i18n.FieldServiceAudienceInvalid,
			agentaction.ValidationExecutionInvalid:         i18n.FieldAgentExecutionInvalid,
			agentaction.ValidationKnowledgeBaseInvalid:     i18n.FieldAgentKnowledgeBaseInvalid,
			agentaction.ValidationModelInvalid:             i18n.FieldChatModelInvalid,
			agentaction.ValidationSystemInstructionTooLong: i18n.FieldAgentSystemInstructionTooLong,
		})
	}
	slog.Info("AI 员工创建成功",
		"organization_id", identity.Organization.ID,
		"identity_id", created.IdentityID,
		"agent_id", created.ID,
		"revision_id", created.Execution.RevisionID,
		"execution_mode", created.Execution.Mode,
		"model_id", created.Execution.Managed.Model.ID,
		"knowledge_base_count", len(created.Execution.Managed.KnowledgeBaseIDs),
	)
	return o.agentWithAvatar(ctx, meta, identity, *created, i18n.ErrorAgentCreateFailed)
}

// ListAgentMCPServerOptions 读取企业 MCP 服务摘要。
func (o *directOperations) ListAgentMCPServerOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AgentMCPServerOptionList, error) {
	options, err := o.listAgentMCPServerOptions.Execute(ctx, identity)
	if err != nil {
		return appservice.AgentMCPServerOptionList{}, o.agentError(ctx, meta, err, i18n.ErrorMCPServerListFailed, identity.Organization.ID, "", nil)
	}
	output := make([]appservice.AgentMCPServerOption, 0, len(options))
	for _, option := range options {
		output = append(output, appservice.AgentMCPServerOption{ID: option.ID, Name: option.Name, ToolCount: option.ToolCount, CustomerScoped: option.CustomerScoped})
	}
	return appservice.AgentMCPServerOptionList{MCPServers: output}, nil
}

// ListAgents 返回企业 AI 员工目录。
func (o *directOperations) ListAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AgentListInput) (appservice.AgentList, error) {
	output, err := o.listAgents.Execute(ctx, identity, agentaction.ListInput{
		Query: input.Query, Status: optionalDomain[appservice.UserStatus, domain.IdentityStatus](input.Status), Page: input.Page, PageSize: input.PageSize,
	})
	if errors.Is(err, agentaction.ErrQueryInvalid) {
		return appservice.AgentList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.AgentList{}, o.agentError(ctx, meta, err, i18n.ErrorAgentListFailed, identity.Organization.ID, "", nil)
	}
	avatarFileIDs := make([]*string, 0, len(output.Agents))
	for _, agent := range output.Agents {
		avatarFileIDs = append(avatarFileIDs, agent.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.AgentList{}, o.agentError(ctx, meta, err, i18n.ErrorAgentListFailed, identity.Organization.ID, "", nil)
	}
	now := time.Now()
	agents := make([]appservice.AgentListItem, 0, len(output.Agents))
	for _, agent := range output.Agents {
		// 转换 AI 员工目录项契约。
		teams := make([]appservice.TeamSummary, 0, len(agent.Teams))
		for _, team := range agent.Teams {
			teams = append(teams, appservice.TeamSummary{ID: team.ID, Name: team.Name})
		}
		// 转换 AI 员工执行配置摘要契约。
		var managed *appservice.AgentManagedExecutionSummary
		if agent.Execution.Managed != nil {
			managed = &appservice.AgentManagedExecutionSummary{Model: aiModelOptionFromAction(agent.Execution.Managed.Model)}
		}
		execution := appservice.AgentExecutionSummary{RevisionID: agent.Execution.RevisionID, Mode: appservice.AgentExecutionMode(agent.Execution.Mode), Managed: managed}
		serviceAudiences := make([]appservice.ServiceAudience, 0, len(agent.ServiceAudiences))
		for _, audience := range agent.ServiceAudiences {
			serviceAudiences = append(serviceAudiences, appservice.ServiceAudience(audience))
		}
		// 个人 AI 员工附带使用的电脑名称与按当前时间计算的在线状态。
		var personal *appservice.AgentListPersonalItem
		if agent.Personal() {
			personal = &appservice.AgentListPersonalItem{ComputerID: common.StringValue(agent.ComputerID), ComputerName: common.StringValue(agent.ComputerName), Presence: appservice.PersonalAgentPresence(agent.Presence(now))}
		}
		agents = append(agents, appservice.AgentListItem{ID: agent.ID, IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, AvatarURL: optionalFileURL(avatarURLs, agent.AvatarFileID), ServiceAudiences: serviceAudiences, Status: appservice.UserStatus(agent.Status), WorkStatus: appservice.WorkStatus(agent.WorkStatus), Teams: teams, Execution: execution, Personal: personal, CreatedAt: agent.CreatedAt})
	}
	return appservice.AgentList{Agents: agents, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetAgent 返回企业 AI 员工详情。
func (o *directOperations) GetAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	agent, err := o.getAgent.Execute(ctx, identity, agentID)
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, i18n.ErrorAgentReadFailed, identity.Organization.ID, agentID, nil)
	}
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentReadFailed)
}

// UpdateAgent 保存企业 AI 员工基本资料、服务对象、转人工团队、负责人、头像和工作状态。
func (o *directOperations) UpdateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.UpdateAgentInput) (appservice.Agent, error) {
	agent, err := o.updateAgent.Execute(ctx, identity, agentID, agentaction.UpdateInput{
		DisplayName: input.DisplayName, TeamIDs: input.TeamIDs, ServiceAudiences: serviceAudiencesInput(input.ServiceAudiences),
		HandoffTeamID: input.HandoffTeamID, ResponsibleUserID: input.ResponsibleUserID,
		WorkStatus: domain.WorkStatus(input.WorkStatus), AvatarFileID: input.AvatarFileID,
	})
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, i18n.ErrorAgentUpdateFailed, identity.Organization.ID, agentID, map[common.FieldCode]i18n.Key{
			agentaction.ValidationDisplayNameRequired:    i18n.FieldAgentNameRequired,
			agentaction.ValidationDisplayNameInvalid:     i18n.FieldDisplayNameInvalid,
			agentaction.ValidationTeamInvalid:            i18n.FieldTeamInvalid,
			agentaction.ValidationServiceAudienceInvalid: i18n.FieldServiceAudienceInvalid,
			agentaction.ValidationHandoffTeamInvalid:     i18n.FieldTeamInvalid,
			agentaction.ValidationResponsibleInvalid:     i18n.FieldAgentResponsibleInvalid,
			agentaction.ValidationWorkStatusInvalid:      i18n.FieldWorkStatusInvalid,
			agentaction.ValidationWorkStatusUnavailable:  i18n.FieldAgentWorkStatusUnavailable,
		})
	}
	slog.Info("AI 员工已保存", "organization_id", identity.Organization.ID, "identity_id", agent.IdentityID, "agent_id", agentID, "work_status", agent.WorkStatus)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentUpdateFailed)
}

// UpdateAgentExecution 修改企业 AI 员工的执行配置。
func (o *directOperations) UpdateAgentExecution(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.UpdateAgentExecutionInput) (appservice.Agent, error) {
	agent, err := o.updateAgentExecution.Execute(ctx, identity, agentID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentExecutionInput(appservice.AgentExecutionInput{Mode: input.Mode, Managed: input.Managed}),
		MCPServerIDs:   input.MCPServerIDs,
	})
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, i18n.ErrorAgentExecutionUpdateFailed, identity.Organization.ID, agentID, map[common.FieldCode]i18n.Key{
			agentaction.ValidationMCPServerInvalid:         i18n.FieldAgentMCPServerInvalid,
			agentaction.ValidationExecutionInvalid:         i18n.FieldAgentExecutionInvalid,
			agentaction.ValidationKnowledgeBaseInvalid:     i18n.FieldAgentKnowledgeBaseInvalid,
			agentaction.ValidationModelInvalid:             i18n.FieldChatModelInvalid,
			agentaction.ValidationSystemInstructionTooLong: i18n.FieldAgentSystemInstructionTooLong,
		})
	}
	slog.Info("AI 员工执行配置已保存",
		"organization_id", identity.Organization.ID,
		"identity_id", agent.IdentityID,
		"agent_id", agentID,
		"revision_id", agent.Execution.RevisionID,
		"execution_mode", agent.Execution.Mode,
		"model_id", agent.Execution.Managed.Model.ID,
		"knowledge_base_count", len(agent.Execution.Managed.KnowledgeBaseIDs),
		"mcp_server_count", len(agent.Execution.MCPServerIDs),
	)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentExecutionUpdateFailed)
}

// DeactivateAgent 禁用企业 AI 员工账号。
func (o *directOperations) DeactivateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	return o.changeAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusInactive)
}

// ReactivateAgent 恢复企业 AI 员工。
func (o *directOperations) ReactivateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	return o.changeAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusActive)
}

// changeAgentStatus 修改企业 AI 员工账号状态。
func (o *directOperations) changeAgentStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (appservice.Agent, error) {
	agent, err := o.updateAgentStatus.Execute(ctx, identity, agentID, status)
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, i18n.ErrorAgentStatusUpdateFailed, identity.Organization.ID, agentID, map[common.FieldCode]i18n.Key{
			agentaction.ValidationStatusInvalid: i18n.FieldUserStatusInvalid,
		})
	}
	slog.Info("AI 员工账号状态已修改", "organization_id", identity.Organization.ID, "identity_id", agent.IdentityID, "agent_id", agentID, "status", status)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentStatusUpdateFailed)
}

// agentWithAvatar 解析 AI 员工头像地址并转换详情契约。
func (o *directOperations) agentWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agent agentaction.Agent, failureKey i18n.Key) (appservice.Agent, error) {
	avatarURLs, err := o.optionalFileURLs(ctx, identity, agent.AvatarFileID)
	if err != nil {
		return appservice.Agent{}, o.agentError(ctx, meta, err, failureKey, identity.Organization.ID, agent.ID, nil)
	}
	output := agentFromAction(agent, identity.Organization.Name)
	output.AvatarURL = optionalFileURL(avatarURLs, agent.AvatarFileID)
	return output, nil
}

// agentFromAction 转换 AI 员工契约，并按服务对象是否包含客户附上内置工作规则。
func agentFromAction(agent agentaction.Agent, organizationName string) appservice.Agent {
	teams := make([]appservice.TeamSummary, 0, len(agent.Teams))
	for _, team := range agent.Teams {
		teams = append(teams, appservice.TeamSummary{ID: team.ID, Name: team.Name})
	}
	// 转换 AI 员工执行配置契约。
	var managed *appservice.AgentManagedExecution
	if agent.Execution.Managed != nil {
		managed = &appservice.AgentManagedExecution{
			Model:             aiModelOptionFromAction(agent.Execution.Managed.Model),
			SystemInstruction: agent.Execution.Managed.SystemInstruction,
			KnowledgeBaseIDs:  agent.Execution.Managed.KnowledgeBaseIDs,
		}
	}
	execution := appservice.AgentExecution{MCPServerIDs: agent.Execution.MCPServerIDs, RevisionID: agent.Execution.RevisionID, Mode: appservice.AgentExecutionMode(agent.Execution.Mode), Managed: managed}
	serviceAudiences := make([]appservice.ServiceAudience, 0, len(agent.ServiceAudiences))
	for _, audience := range agent.ServiceAudiences {
		serviceAudiences = append(serviceAudiences, appservice.ServiceAudience(audience))
	}
	instruction, tools := agentrunaction.BehaviorProfile(slices.Contains(agent.ServiceAudiences, domain.ServiceAudienceCustomer), organizationName)
	behavior := appservice.AgentBehaviorProfile{Instruction: instruction, Tools: tools}
	var responsible *appservice.AgentResponsible
	if agent.Responsible != nil {
		responsible = &appservice.AgentResponsible{
			UserID: agent.Responsible.UserID, DisplayName: agent.Responsible.DisplayName,
			Email: agent.Responsible.Email, Status: appservice.UserStatus(agent.Responsible.Status),
		}
	}
	return appservice.Agent{ID: agent.ID, IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, ServiceAudiences: serviceAudiences, HandoffTeamID: agent.HandoffTeamID, Responsible: responsible, Status: appservice.UserStatus(agent.Status), WorkStatus: appservice.WorkStatus(agent.WorkStatus), Teams: teams, Execution: execution, Behavior: behavior, CreatedAt: agent.CreatedAt}
}

// serviceAudiencesInput 转换服务对象输入。
func serviceAudiencesInput(values []appservice.ServiceAudience) []domain.ServiceAudience {
	audiences := make([]domain.ServiceAudience, 0, len(values))
	for _, value := range values {
		audiences = append(audiences, domain.ServiceAudience(value))
	}
	return audiences
}

// agentExecutionInput 转换 AI 员工执行配置输入。
func agentExecutionInput(input appservice.AgentExecutionInput) agentaction.ExecutionInput {
	var managed *agentaction.ManagedExecutionInput
	if input.Managed != nil {
		managed = &agentaction.ManagedExecutionInput{
			ModelID:           input.Managed.ModelID,
			SystemInstruction: input.Managed.SystemInstruction,
			KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
		}
	}
	return agentaction.ExecutionInput{Mode: domain.AgentExecutionMode(input.Mode), Managed: managed}
}

// agentError 转换 AI 员工领域错误并记录未处理故障。
func (o *directOperations) agentError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, agentID string, fieldKeys map[common.FieldCode]i18n.Key) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, fieldKeys))
	}
	if errors.Is(err, agentaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorAgentNotFound)
	}
	if errors.Is(err, fileaction.ErrLinkedImageNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	attributes := []any{"organization_id", organizationID, "failure", failureKey, "error", err}
	if agentID != "" {
		attributes = append(attributes, "agent_id", agentID)
	}
	slog.Warn("AI 员工操作失败", attributes...)
	return appservice.FailedError(meta, failureKey)
}
