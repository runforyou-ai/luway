//go:build server

package direct

import (
	"context"
	"log/slog"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// personalAgentOps 持有个人 AI 员工的 Action 和 Query。
type personalAgentOps struct {
	listPersonalAgents        *agentaction.ListPersonalAgentsQuery
	getPersonalAgent          *agentaction.GetPersonalAgentQuery
	createPersonalAgent       *agentaction.CreatePersonalAgentAction
	updatePersonalAgent       *agentaction.UpdatePersonalAgentAction
	setPersonalAgentPaused    *agentaction.SetPersonalAgentPausedAction
	movePersonalAgent         *agentaction.MovePersonalAgentAction
	updatePersonalAgentStatus *agentaction.UpdatePersonalAgentStatusAction
	listMemories              *agentaction.ListAgentMemoriesQuery
	updateMemory              *agentaction.UpdateAgentMemoryAction
	deleteMemory              *agentaction.DeleteAgentMemoryAction
	// files 解析文件地址。
	files *fileOps
}

// newPersonalAgentOps 创建个人 AI 员工的业务实现依赖。
func newPersonalAgentOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, files *fileOps) *personalAgentOps {
	return &personalAgentOps{
		listPersonalAgents:        agentaction.NewListPersonalAgentsQuery(db),
		getPersonalAgent:          agentaction.NewGetPersonalAgentQuery(db),
		createPersonalAgent:       agentaction.NewCreatePersonalAgentAction(db),
		updatePersonalAgent:       agentaction.NewUpdatePersonalAgentAction(db),
		setPersonalAgentPaused:    agentaction.NewSetPersonalAgentPausedAction(db),
		movePersonalAgent:         agentaction.NewMovePersonalAgentAction(db),
		updatePersonalAgentStatus: agentaction.NewUpdatePersonalAgentStatusAction(db, taskEnqueuer),
		listMemories:              agentaction.NewListAgentMemoriesQuery(db),
		updateMemory:              agentaction.NewUpdateAgentMemoryAction(db),
		deleteMemory:              agentaction.NewDeleteAgentMemoryAction(db),
		files:                     files,
	}
}

// personalAgentFieldKeys 是个人 AI 员工资料与执行配置的字段校验文案。
var personalAgentFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationDisplayNameRequired:   i18n.FieldAgentNameRequired,
	agentaction.ValidationDisplayNameInvalid:    i18n.FieldDisplayNameInvalid,
	agentaction.ValidationExecutionInvalid:      i18n.FieldAgentExecutionInvalid,
	agentaction.ValidationKnowledgeBaseInvalid:  i18n.FieldAgentKnowledgeBaseInvalid,
	agentaction.ValidationBusinessSystemInvalid: i18n.FieldAgentBusinessSystemInvalid,
	agentaction.ValidationModelInvalid:          i18n.FieldChatModelInvalid,
	agentaction.ValidationStatusInvalid:         i18n.FieldUserStatusInvalid,
}

// ListPersonalAgents 返回当前成员负责的个人 AI 员工。
func (o *personalAgentOps) ListPersonalAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.PersonalAgentList, error) {
	return o.listResponsiblePersonalAgents(ctx, meta, identity, identity.User.ID)
}

// ListMemberPersonalAgents 返回指定成员负责的个人 AI 员工。
func (o *personalAgentOps) ListMemberPersonalAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.PersonalAgentList, error) {
	return o.listResponsiblePersonalAgents(ctx, meta, identity, userID)
}

// listResponsiblePersonalAgents 读取指定成员负责的个人 AI 员工并解析头像地址。
func (o *personalAgentOps) listResponsiblePersonalAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.PersonalAgentList, error) {
	records, err := o.listPersonalAgents.Execute(ctx, identity, userID)
	if err != nil {
		return appservice.PersonalAgentList{}, personalAgentError(meta, err, i18n.ErrorAgentListFailed)
	}
	avatarFileIDs := arr.Map(records, func(record agentaction.PersonalAgent) *string { return record.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.PersonalAgentList{}, personalAgentError(meta, err, i18n.ErrorAgentListFailed)
	}
	personalAgents := arr.Map(records, func(record agentaction.PersonalAgent) appservice.PersonalAgent {
		return personalAgentFromAction(record, optionalFileURL(avatarURLs, record.AvatarFileID))
	})
	return appservice.PersonalAgentList{PersonalAgents: personalAgents}, nil
}

// GetPersonalAgent 返回当前成员负责的个人 AI 员工详情。
func (o *personalAgentOps) GetPersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.PersonalAgentDetail, error) {
	record, execution, err := o.getPersonalAgent.Execute(ctx, identity, agentID)
	if err != nil {
		return appservice.PersonalAgentDetail{}, personalAgentError(meta, err, i18n.ErrorAgentReadFailed)
	}
	personalAgent, err := o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentReadFailed)
	if err != nil {
		return appservice.PersonalAgentDetail{}, err
	}
	// 转换个人 AI 员工的完整执行配置契约。
	output := appservice.AgentExecution{BusinessSystems: businessSystemGrantsFromAction(execution.BusinessSystems), RevisionID: execution.RevisionID, Mode: execution.Mode}
	if execution.Managed != nil {
		output.Managed = &appservice.AgentManagedExecution{
			Model:             aiModelOptionFromAction(execution.Managed.Model),
			SystemInstruction: execution.Managed.SystemInstruction, KnowledgeBaseIDs: execution.Managed.KnowledgeBaseIDs,
		}
	}
	return appservice.PersonalAgentDetail{PersonalAgent: personalAgent, Execution: output}, nil
}

// CreatePersonalAgent 在当前成员的电脑上创建个人 AI 员工。
func (o *personalAgentOps) CreatePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreatePersonalAgentInput) (appservice.PersonalAgent, error) {
	record, err := o.createPersonalAgent.Execute(ctx, identity, input.ComputerID, agentaction.PersonalAgentInput{
		DisplayName: input.DisplayName, AvatarFileID: input.AvatarFileID, Execution: personalAgentExecutionInput(input.Execution),
		BusinessSystems: businessSystemGrantsInput(input.BusinessSystems), LocalAgents: input.LocalAgents,
	})
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, i18n.ErrorAgentCreateFailed)
	}
	return o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentCreateFailed)
}

// UpdatePersonalAgent 修改当前成员负责的个人 AI 员工。
func (o *personalAgentOps) UpdatePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.PersonalAgentInput) (appservice.PersonalAgent, error) {
	record, err := o.updatePersonalAgent.Execute(ctx, identity, agentID, agentaction.PersonalAgentInput{
		DisplayName: input.DisplayName, AvatarFileID: input.AvatarFileID, Execution: personalAgentExecutionInput(input.Execution),
		BusinessSystems: businessSystemGrantsInput(input.BusinessSystems), LocalAgents: input.LocalAgents,
	})
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, i18n.ErrorAgentUpdateFailed)
	}
	slog.InfoContext(ctx, "个人 AI 员工已保存", "agent_id", agentID, "revision_id", record.Execution.RevisionID)
	return o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentUpdateFailed)
}

// PausePersonalAgent 暂停当前成员负责的个人 AI 员工。
func (o *personalAgentOps) PausePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.PersonalAgent, error) {
	return o.changePersonalAgentPaused(ctx, meta, identity, agentID, true)
}

// ResumePersonalAgent 恢复当前成员名下已暂停的个人 AI 员工。
func (o *personalAgentOps) ResumePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.PersonalAgent, error) {
	return o.changePersonalAgentPaused(ctx, meta, identity, agentID, false)
}

// changePersonalAgentPaused 修改个人 AI 员工的暂停状态。
func (o *personalAgentOps) changePersonalAgentPaused(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, paused bool) (appservice.PersonalAgent, error) {
	record, err := o.setPersonalAgentPaused.Execute(ctx, identity, agentID, paused)
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, i18n.ErrorAgentPauseFailed)
	}
	slog.InfoContext(ctx, "个人 AI 员工暂停状态已修改", "agent_id", agentID, "paused", paused)
	return o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentPauseFailed)
}

// MovePersonalAgent 把当前成员负责的个人 AI 员工换到指定电脑。
func (o *personalAgentOps) MovePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, input appservice.PersonalAgentComputerInput) (appservice.PersonalAgent, error) {
	record, err := o.movePersonalAgent.Execute(ctx, identity, agentID, input.ComputerID)
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, i18n.ErrorAgentMoveFailed)
	}
	return o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentMoveFailed)
}

// DeactivatePersonalAgent 停用个人 AI 员工。
func (o *personalAgentOps) DeactivatePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.PersonalAgent, error) {
	return o.changePersonalAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusInactive)
}

// ReactivatePersonalAgent 启用已停用的个人 AI 员工。
func (o *personalAgentOps) ReactivatePersonalAgent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.PersonalAgent, error) {
	return o.changePersonalAgentStatus(ctx, meta, identity, agentID, domain.IdentityStatusActive)
}

// changePersonalAgentStatus 修改个人 AI 员工的账号状态。
func (o *personalAgentOps) changePersonalAgentStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (appservice.PersonalAgent, error) {
	record, err := o.updatePersonalAgentStatus.Execute(ctx, identity, agentID, status)
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, i18n.ErrorAgentStatusUpdateFailed)
	}
	slog.InfoContext(ctx, "个人 AI 员工状态已修改", "agent_id", agentID, "status", status)
	return o.personalAgentWithAvatar(ctx, meta, identity, record, i18n.ErrorAgentStatusUpdateFailed)
}

// personalAgentWithAvatar 解析个人 AI 员工头像地址并转换契约。
func (o *personalAgentOps) personalAgentWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, record *agentaction.PersonalAgent, failureKey i18n.Key) (appservice.PersonalAgent, error) {
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, record.AvatarFileID)
	if err != nil {
		return appservice.PersonalAgent{}, personalAgentError(meta, err, failureKey)
	}
	return personalAgentFromAction(*record, optionalFileURL(avatarURLs, record.AvatarFileID)), nil
}

// ListAgentMemories 返回当前成员名下个人 AI 员工的记忆，按最近更新排列。
func (o *personalAgentOps) ListAgentMemories(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID string) (appservice.AgentMemoryList, error) {
	records, err := o.listMemories.Execute(ctx, identity, agentID)
	if err != nil {
		return appservice.AgentMemoryList{}, personalAgentError(meta, err, i18n.ErrorMemoryListFailed)
	}
	memories := arr.Map(records, agentMemoryFromAction)
	return appservice.AgentMemoryList{Memories: memories}, nil
}

// UpdateAgentMemory 修改当前成员名下个人 AI 员工的一条记忆。
func (o *personalAgentOps) UpdateAgentMemory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, memoryID string, input appservice.AgentMemoryInput) (appservice.AgentMemory, error) {
	record, err := o.updateMemory.Execute(ctx, identity, agentID, memoryID, agentaction.AgentMemoryInput{
		Name: input.Name, Description: input.Description, Body: input.Body,
	})
	if err != nil {
		return appservice.AgentMemory{}, personalAgentError(meta, err, i18n.ErrorMemoryUpdateFailed)
	}
	slog.InfoContext(ctx, "个人 AI 员工记忆已修改", "agent_id", agentID, "memory_id", memoryID)
	return agentMemoryFromAction(*record), nil
}

// DeleteAgentMemory 删除当前成员名下个人 AI 员工的一条记忆。
func (o *personalAgentOps) DeleteAgentMemory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, agentID, memoryID string) error {
	if err := o.deleteMemory.Execute(ctx, identity, agentID, memoryID); err != nil {
		return personalAgentError(meta, err, i18n.ErrorMemoryDeleteFailed)
	}
	slog.InfoContext(ctx, "个人 AI 员工记忆已删除", "agent_id", agentID, "memory_id", memoryID)
	return nil
}

// agentMemoryFromAction 转换个人 AI 员工记忆契约。
func agentMemoryFromAction(record agentaction.AgentMemory) appservice.AgentMemory {
	return appservice.AgentMemory{ID: record.ID, Name: record.Name, Description: record.Description, Body: record.Body, UpdatedAt: record.UpdatedAt}
}

// personalAgentErrors 是个人 AI 员工操作的错误转换规则。
var personalAgentErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(personalAgentFieldKeys),
	dispatch.Is(agentaction.ErrPersonalAgentNotFound, dispatch.NotFound(i18n.ErrorAgentNotFound)),
	dispatch.Is(agentaction.ErrAgentMemoryNotFound, dispatch.NotFound(i18n.ErrorMemoryNotFound)),
	dispatch.Is(agentaction.ErrPersonalAgentComputerNotFound, dispatch.NotFound(i18n.ErrorComputerNotFound)),
	dispatch.Is(agentaction.ErrPersonalAgentForbidden, dispatch.Forbidden(i18n.ErrorPermissionDenied)),
	dispatch.Is(agentaction.ErrPersonalAgentResponsibleInactive, dispatch.Conflict(i18n.ErrorAgentResponsibleInactive, "personal_agent_responsible_inactive")),
	dispatch.Is(fileaction.ErrLinkedImageNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
})

// personalAgentError 转换个人 AI 员工操作错误。
func personalAgentError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return personalAgentErrors.Translate(meta, err, failureKey)
}

// personalAgentExecutionInput 转换个人 AI 员工的执行配置输入。
func personalAgentExecutionInput(input appservice.AgentExecutionInput) agentaction.ExecutionInput {
	output := agentaction.ExecutionInput{Mode: input.Mode}
	if input.Managed != nil {
		output.Managed = &agentaction.ManagedExecutionInput{
			ModelID:           input.Managed.ModelID,
			SystemInstruction: input.Managed.SystemInstruction, KnowledgeBaseIDs: input.Managed.KnowledgeBaseIDs,
		}
	}
	return output
}

// personalAgentFromAction 转换个人 AI 员工契约。
func personalAgentFromAction(record agentaction.PersonalAgent, avatarURL string) appservice.PersonalAgent {
	execution := appservice.AgentExecutionSummary{RevisionID: record.Execution.RevisionID, Mode: record.Execution.Mode}
	if record.Execution.Managed != nil {
		execution.Managed = &appservice.AgentManagedExecutionSummary{
			Model: aiModelOptionFromAction(record.Execution.Managed.Model),
		}
	}
	return appservice.PersonalAgent{
		ID: record.ID, IdentityID: record.IdentityID, DisplayName: record.DisplayName, AvatarURL: avatarURL,
		Responsible: appservice.PersonalAgentResponsible{UserID: record.ResponsibleUserID, IdentityID: record.ResponsibleIdentityID, DisplayName: record.ResponsibleDisplayName},
		Computer:    appservice.PersonalAgentComputer{ID: record.ComputerID, Name: record.ComputerName, LocalAgents: computerLocalAgents(record.ComputerLocalAgents)},
		LocalAgents: append([]string{}, record.LocalAgents...),
		Status:      appservice.UserStatus(record.Status),
		Presence:    record.Presence(),
		Execution:   execution,
		CreatedAt:   record.CreatedAt,
	}
}
