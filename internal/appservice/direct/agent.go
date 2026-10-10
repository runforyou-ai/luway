//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// agentOps 持有 AI 员工与运行的 Action 和 Query。
type agentOps struct {
	runCancellation                *agentrunaction.RunCancellation
	serviceReplySuggestions        *agentrunaction.GenerateServiceReplySuggestionsAction
	listServiceReplyAgents         *agentrunaction.ListServiceReplyAgentsQuery
	listAgentBusinessSystemOptions *agentaction.ListBusinessSystemOptionsQuery
	createAgent                    *agentaction.CreateAgentAction
	listAgents                     *agentaction.ListAgentsQuery
	getAgent                       *agentaction.GetAgentQuery
	updateAgent                    *agentaction.UpdateAgentAction
	updateAgentExecution           *agentaction.UpdateExecutionAction
	updateAgentStatus              *agentaction.UpdateStatusAction
	// files 解析文件地址。
	files *fileOps
}

// newAgentOps 创建 AI 员工与运行的业务实现依赖。
func newAgentOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, runCancellation *agentrunaction.RunCancellation, returner *servicehandoffaction.Returner, serviceReplySuggestions *agentrunaction.GenerateServiceReplySuggestionsAction, files *fileOps) *agentOps {
	return &agentOps{
		runCancellation:                runCancellation,
		serviceReplySuggestions:        serviceReplySuggestions,
		listServiceReplyAgents:         agentrunaction.NewListServiceReplyAgentsQuery(db),
		listAgentBusinessSystemOptions: agentaction.NewListBusinessSystemOptionsQuery(db),
		createAgent:                    agentaction.NewCreateAgentAction(db),
		listAgents:                     agentaction.NewListAgentsQuery(db),
		getAgent:                       agentaction.NewGetAgentQuery(db),
		updateAgent:                    agentaction.NewUpdateAgentAction(db, taskEnqueuer, returner),
		updateAgentExecution:           agentaction.NewUpdateExecutionAction(db),
		updateAgentStatus:              agentaction.NewUpdateStatusAction(db, taskEnqueuer, returner),
		files:                          files,
	}
}

// CreateAgent 创建企业 AI 员工。
func (o *agentOps) CreateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreateAgentInput) (appservice.Agent, error) {
	created, err := o.createAgent.Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: input.DisplayName, TeamIDs: input.TeamIDs,
		ServiceAudiences: serviceAudiencesInput(input.ServiceAudiences), AvatarFileID: input.AvatarFileID,
		Execution: agentExecutionInput(input.Execution),
	})
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, i18n.ErrorAgentCreateFailed, agentCreateFieldKeys)
	}
	slog.InfoContext(ctx, "AI 员工创建成功",
		"identity_id", created.IdentityID,
		"agent_id", created.ID,
		"revision_id", created.Execution.RevisionID,
		"execution_mode", created.Execution.Mode,
		"model_id", created.Execution.Managed.Model.ID,
		"knowledge_base_count", len(created.Execution.Managed.KnowledgeBaseIDs),
	)
	return o.agentWithAvatar(ctx, meta, identity, *created, i18n.ErrorAgentCreateFailed)
}

// ListAgentBusinessSystemOptions 读取工作区业务系统摘要。
func (o *agentOps) ListAgentBusinessSystemOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AgentBusinessSystemOptionList, error) {
	options, err := o.listAgentBusinessSystemOptions.Execute(ctx, identity)
	if err != nil {
		return appservice.AgentBusinessSystemOptionList{}, agentError(meta, err, i18n.ErrorBusinessSystemListFailed, nil)
	}
	output := arr.Map(options, func(option agentaction.BusinessSystemOption) appservice.AgentBusinessSystemOption {
		return appservice.AgentBusinessSystemOption{ID: option.ID, Name: option.Name, ToolCount: option.ToolCount}
	})
	return appservice.AgentBusinessSystemOptionList{BusinessSystems: output}, nil
}

// businessSystemGrantsInput 转换 AI 员工的业务系统授权输入。
func businessSystemGrantsInput(grants []appservice.AgentBusinessSystemGrant) []domain.BusinessSystemGrant {
	output := make([]domain.BusinessSystemGrant, 0, len(grants))
	for _, grant := range grants {
		output = append(output, domain.BusinessSystemGrant{
			BusinessSystemID: grant.BusinessSystemID, ToolGrant: domain.ToolGrant{MaxLevel: grant.MaxLevel, ConfirmL2: grant.ConfirmL2}, Outbound: grant.Outbound,
		})
	}
	return output
}

// businessSystemGrantsFromAction 转换 AI 员工的业务系统授权。
func businessSystemGrantsFromAction(grants []domain.BusinessSystemGrant) []appservice.AgentBusinessSystemGrant {
	return arr.Map(grants, func(grant domain.BusinessSystemGrant) appservice.AgentBusinessSystemGrant {
		return appservice.AgentBusinessSystemGrant{
			BusinessSystemID: grant.BusinessSystemID, MaxLevel: grant.MaxLevel, ConfirmL2: grant.ConfirmL2, Outbound: grant.Outbound,
		}
	})
}

// ListAgents 返回企业 AI 员工目录。
func (o *agentOps) ListAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AgentListInput) (appservice.AgentList, error) {
	output, err := o.listAgents.Execute(ctx, identity, agentaction.ListInput{
		Query: input.Query, Status: domain.IdentityStatus(support.Deref(input.Status)), Page: input.Page, PageSize: input.PageSize,
	})
	if errors.Is(err, agentaction.ErrQueryInvalid) {
		return appservice.AgentList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	if err != nil {
		return appservice.AgentList{}, agentError(meta, err, i18n.ErrorAgentListFailed, nil)
	}
	avatarFileIDs := arr.Map(output.Agents, func(agent agentaction.ListItem) *string { return agent.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.AgentList{}, agentError(meta, err, i18n.ErrorAgentListFailed, nil)
	}
	agents := make([]appservice.AgentListItem, 0, len(output.Agents))
	for _, agent := range output.Agents {
		// 转换 AI 员工目录项契约。
		teams := arr.Map(agent.Teams, func(team agentaction.TeamSummary) appservice.TeamSummary {
			return appservice.TeamSummary{ID: team.ID, Name: team.Name}
		})
		// 转换 AI 员工执行配置摘要契约。
		managed := support.MapPtr(agent.Execution.Managed, func(managed agentaction.ManagedExecutionSummary) appservice.AgentManagedExecutionSummary {
			return appservice.AgentManagedExecutionSummary{Model: aiModelOptionFromAction(managed.Model)}
		})
		execution := appservice.AgentExecutionSummary{RevisionID: agent.Execution.RevisionID, Mode: agent.Execution.Mode, Managed: managed}
		serviceAudiences := append(make([]appservice.ServiceAudience, 0, len(agent.ServiceAudiences)), agent.ServiceAudiences...)
		// 个人 AI 员工附带使用的电脑名称与按当前时间计算的在线状态。
		var personal *appservice.AgentListPersonalItem
		if agent.Personal() {
			personal = &appservice.AgentListPersonalItem{ComputerID: support.Deref(agent.ComputerID), ComputerName: support.Deref(agent.ComputerName), Presence: agent.Presence()}
		}
		agents = append(agents, appservice.AgentListItem{ID: agent.ID, IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, AvatarURL: optionalFileURL(avatarURLs, agent.AvatarFileID), ServiceAudiences: serviceAudiences, Status: appservice.UserStatus(agent.Status), WorkStatus: agent.WorkStatus, Teams: teams, Execution: execution, Personal: personal, CreatedAt: agent.CreatedAt})
	}
	return appservice.AgentList{Agents: agents, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetAgent 返回企业 AI 员工详情。
func (o *agentOps) GetAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	agent, err := o.getAgent.Execute(ctx, identity, agentID)
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, i18n.ErrorAgentReadFailed, nil)
	}
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentReadFailed)
}

// GetAgentProfile 返回企业 AI 员工的公开资料。
func (o *agentOps) GetAgentProfile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.AgentProfile, error) {
	agent, err := o.GetAgent(ctx, meta, identity, agentID)
	if err != nil {
		return appservice.AgentProfile{}, err
	}
	return appservice.AgentProfile{ID: agent.ID, IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, AvatarURL: agent.AvatarURL, Status: agent.Status}, nil
}

// UpdateAgent 保存企业 AI 员工基本资料、服务对象、转人工团队、负责人、工作区电脑、头像和工作状态。
func (o *agentOps) UpdateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.UpdateAgentInput) (appservice.Agent, error) {
	agent, err := o.updateAgent.Execute(ctx, identity, agentID, agentaction.UpdateInput{
		DisplayName: input.DisplayName, TeamIDs: input.TeamIDs, ServiceAudiences: serviceAudiencesInput(input.ServiceAudiences),
		HandoffTeamID: input.HandoffTeamID, ResponsibleUserID: input.ResponsibleUserID, ComputerID: input.ComputerID,
		ComputerGrant: domain.ToolGrant{MaxLevel: input.ComputerGrant.MaxLevel, ConfirmL2: input.ComputerGrant.ConfirmL2},
		LocalAgents:   input.LocalAgents,
		WorkStatus:    input.WorkStatus, AvatarFileID: input.AvatarFileID,
	})
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, i18n.ErrorAgentUpdateFailed, agentUpdateFieldKeys)
	}
	slog.InfoContext(ctx, "AI 员工已保存", "identity_id", agent.IdentityID, "agent_id", agentID, "work_status", agent.WorkStatus)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentUpdateFailed)
}

// UpdateAgentExecution 修改企业 AI 员工的执行配置。
func (o *agentOps) UpdateAgentExecution(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.UpdateAgentExecutionInput) (appservice.Agent, error) {
	agent, err := o.updateAgentExecution.Execute(ctx, identity, agentID, agentaction.UpdateExecutionInput{
		ExecutionInput:  agentExecutionInput(appservice.AgentExecutionInput{Mode: input.Mode, Managed: input.Managed}),
		BusinessSystems: businessSystemGrantsInput(input.BusinessSystems),
	})
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, i18n.ErrorAgentExecutionUpdateFailed, agentExecutionFieldKeys)
	}
	slog.InfoContext(ctx, "AI 员工执行配置已保存",
		"identity_id", agent.IdentityID,
		"agent_id", agentID,
		"revision_id", agent.Execution.RevisionID,
		"execution_mode", agent.Execution.Mode,
		"model_id", agent.Execution.Managed.Model.ID,
		"knowledge_base_count", len(agent.Execution.Managed.KnowledgeBaseIDs),
		"business_system_count", len(agent.Execution.BusinessSystems),
	)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentExecutionUpdateFailed)
}

// DeactivateAgent 禁用企业 AI 员工账号。
func (o *agentOps) DeactivateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	return o.changeAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusInactive)
}

// ReactivateAgent 恢复企业 AI 员工。
func (o *agentOps) ReactivateAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.Agent, error) {
	return o.changeAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusActive)
}

// changeAgentStatus 修改企业 AI 员工账号状态。
func (o *agentOps) changeAgentStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (appservice.Agent, error) {
	agent, err := o.updateAgentStatus.Execute(ctx, identity, agentID, status)
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, i18n.ErrorAgentStatusUpdateFailed, agentStatusFieldKeys)
	}
	slog.InfoContext(ctx, "AI 员工账号状态已修改", "identity_id", agent.IdentityID, "agent_id", agentID, "status", status)
	return o.agentWithAvatar(ctx, meta, identity, *agent, i18n.ErrorAgentStatusUpdateFailed)
}

// agentWithAvatar 解析 AI 员工头像地址并转换详情契约。
func (o *agentOps) agentWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agent agentaction.Agent, failureKey i18n.Key) (appservice.Agent, error) {
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, agent.AvatarFileID)
	if err != nil {
		return appservice.Agent{}, agentError(meta, err, failureKey, nil)
	}
	output := agentFromAction(agent, identity.Workspace.Name)
	output.AvatarURL = optionalFileURL(avatarURLs, agent.AvatarFileID)
	return output, nil
}

// agentFromAction 转换 AI 员工契约，并按服务对象是否包含客户附上内置工作规则。
func agentFromAction(agent agentaction.Agent, workspaceName string) appservice.Agent {
	teams := arr.Map(agent.Teams, func(team agentaction.TeamSummary) appservice.TeamSummary {
		return appservice.TeamSummary{ID: team.ID, Name: team.Name}
	})
	// 转换 AI 员工执行配置契约。
	var managed *appservice.AgentManagedExecution
	if agent.Execution.Managed != nil {
		managed = &appservice.AgentManagedExecution{
			Model:             aiModelOptionFromAction(agent.Execution.Managed.Model),
			SystemInstruction: agent.Execution.Managed.SystemInstruction,
			KnowledgeBaseIDs:  agent.Execution.Managed.KnowledgeBaseIDs,
		}
	}
	execution := appservice.AgentExecution{BusinessSystems: businessSystemGrantsFromAction(agent.Execution.BusinessSystems), RevisionID: agent.Execution.RevisionID, Mode: agent.Execution.Mode, Managed: managed}
	serviceAudiences := append(make([]appservice.ServiceAudience, 0, len(agent.ServiceAudiences)), agent.ServiceAudiences...)
	var computerGrant *domain.ToolGrant
	if agent.Computer != nil {
		computerGrant = &agent.Computer.Grant
	}
	instruction, tools := agentrunaction.BehaviorProfile(slices.Contains(agent.ServiceAudiences, domain.ServiceAudienceCustomer), computerGrant,
		agent.Computer != nil && len(agent.Computer.LocalAgents) > 0, workspaceName)
	behavior := appservice.AgentBehaviorProfile{Instruction: instruction, Tools: tools}
	var responsible *appservice.AgentResponsible
	if agent.Responsible != nil {
		responsible = &appservice.AgentResponsible{
			UserID: agent.Responsible.UserID, DisplayName: agent.Responsible.DisplayName,
			Email: agent.Responsible.Email, Status: appservice.UserStatus(agent.Responsible.Status),
		}
	}
	var computer *appservice.AgentComputer
	if agent.Computer != nil {
		computer = &appservice.AgentComputer{
			ID: agent.Computer.ID, Name: agent.Computer.Name,
			Grant:       appservice.AgentToolGrant{MaxLevel: agent.Computer.Grant.MaxLevel, ConfirmL2: agent.Computer.Grant.ConfirmL2},
			LocalAgents: append([]string{}, agent.Computer.LocalAgents...),
		}
	}
	return appservice.Agent{ID: agent.ID, IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, ServiceAudiences: serviceAudiences, HandoffTeamID: agent.HandoffTeamID, Responsible: responsible, Computer: computer, Status: appservice.UserStatus(agent.Status), WorkStatus: agent.WorkStatus, Teams: teams, Execution: execution, Behavior: behavior, CreatedAt: agent.CreatedAt}
}

// serviceAudiencesInput 转换服务对象输入。
func serviceAudiencesInput(values []appservice.ServiceAudience) []domain.ServiceAudience {
	audiences := append(make([]domain.ServiceAudience, 0, len(values)), values...)
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
	return agentaction.ExecutionInput{Mode: input.Mode, Managed: managed}
}

// agentError 转换 AI 员工领域错误，fieldKeys 是该操作的校验错误码文案。
func agentError(meta appservice.RequestMeta, err error, failureKey i18n.Key, fieldKeys map[common.FieldCode]i18n.Key) error {
	return dispatch.Catalog{
		dispatch.CommonErrors.Find,
		dispatch.FieldRule(fieldKeys),
		dispatch.Is(agentaction.ErrNotFound, dispatch.NotFound(i18n.ErrorAgentNotFound)),
		dispatch.Is(fileaction.ErrLinkedImageNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	}.Translate(meta, err, failureKey)
}

// agentCreateFieldKeys 把创建 AI 员工的校验错误码映射为本地化文案键。
var agentCreateFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationDisplayNameRequired:  i18n.FieldAgentNameRequired,
	agentaction.ValidationDisplayNameInvalid:   i18n.FieldDisplayNameInvalid,
	agentaction.ValidationTeamInvalid:          i18n.FieldTeamInvalid,
	agentaction.ValidationExecutionInvalid:     i18n.FieldAgentExecutionInvalid,
	agentaction.ValidationKnowledgeBaseInvalid: i18n.FieldAgentKnowledgeBaseInvalid,
	agentaction.ValidationModelInvalid:         i18n.FieldChatModelInvalid,
}

// agentUpdateFieldKeys 把修改 AI 员工资料的校验错误码映射为本地化文案键。
var agentUpdateFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationDisplayNameRequired:   i18n.FieldAgentNameRequired,
	agentaction.ValidationDisplayNameInvalid:    i18n.FieldDisplayNameInvalid,
	agentaction.ValidationTeamInvalid:           i18n.FieldTeamInvalid,
	agentaction.ValidationHandoffTeamInvalid:    i18n.FieldTeamInvalid,
	agentaction.ValidationResponsibleInvalid:    i18n.FieldAgentResponsibleInvalid,
	agentaction.ValidationResponsibleRequired:   i18n.FieldAgentResponsibleRequired,
	agentaction.ValidationComputerInvalid:       i18n.FieldAgentComputerInvalid,
	agentaction.ValidationComputerGrantInvalid:  i18n.FieldAgentComputerGrantInvalid,
	agentaction.ValidationWorkStatusUnavailable: i18n.FieldAgentWorkStatusUnavailable,
}

// agentExecutionFieldKeys 把修改 AI 员工执行配置的校验错误码映射为本地化文案键。
var agentExecutionFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationBusinessSystemInvalid: i18n.FieldAgentBusinessSystemInvalid,
	agentaction.ValidationResponsibleRequired:   i18n.FieldAgentResponsibleRequired,
	agentaction.ValidationExecutionInvalid:      i18n.FieldAgentExecutionInvalid,
	agentaction.ValidationKnowledgeBaseInvalid:  i18n.FieldAgentKnowledgeBaseInvalid,
	agentaction.ValidationModelInvalid:          i18n.FieldChatModelInvalid,
}

// agentStatusFieldKeys 把修改 AI 员工状态的校验错误码映射为本地化文案键。
var agentStatusFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationStatusInvalid: i18n.FieldUserStatusInvalid,
}
