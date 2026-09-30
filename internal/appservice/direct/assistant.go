//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"time"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// assistantOps 持有助理的 Action 和 Query。
type assistantOps struct {
	listAssistants        *agentaction.ListAssistantsQuery
	getAssistant          *agentaction.GetAssistantQuery
	createAssistant       *agentaction.CreateAssistantAction
	updateAssistant       *agentaction.UpdateAssistantAction
	setAssistantPaused    *agentaction.SetAssistantPausedAction
	moveAssistant         *agentaction.MoveAssistantAction
	updateAssistantStatus *agentaction.UpdateAssistantStatusAction
	listMemories          *agentaction.ListAssistantMemoriesQuery
	updateMemory          *agentaction.UpdateAssistantMemoryAction
	deleteMemory          *agentaction.DeleteAssistantMemoryAction
}

// newAssistantOps 创建助理的业务实现依赖。
func newAssistantOps(db *bun.DB) assistantOps {
	return assistantOps{
		listAssistants:        agentaction.NewListAssistantsQuery(db),
		getAssistant:          agentaction.NewGetAssistantQuery(db),
		createAssistant:       agentaction.NewCreateAssistantAction(db),
		updateAssistant:       agentaction.NewUpdateAssistantAction(db),
		setAssistantPaused:    agentaction.NewSetAssistantPausedAction(db),
		moveAssistant:         agentaction.NewMoveAssistantAction(db),
		updateAssistantStatus: agentaction.NewUpdateAssistantStatusAction(db),
		listMemories:          agentaction.NewListAssistantMemoriesQuery(db),
		updateMemory:          agentaction.NewUpdateAssistantMemoryAction(db),
		deleteMemory:          agentaction.NewDeleteAssistantMemoryAction(db),
	}
}

// assistantFieldKeys 是助理资料与执行配置的字段校验文案。
var assistantFieldKeys = map[common.FieldCode]i18n.Key{
	agentaction.ValidationDisplayNameRequired:       i18n.FieldAssistantNameRequired,
	agentaction.ValidationDisplayNameInvalid:        i18n.FieldDisplayNameInvalid,
	agentaction.ValidationExecutionInvalid:          i18n.FieldAgentExecutionInvalid,
	agentaction.ValidationKnowledgeBaseInvalid:      i18n.FieldAgentKnowledgeBaseInvalid,
	agentaction.ValidationMCPServerInvalid:          i18n.FieldAgentMCPServerInvalid,
	agentaction.ValidationModelInvalid:              i18n.FieldChatModelInvalid,
	agentaction.ValidationSystemInstructionTooLong:  i18n.FieldAgentSystemInstructionTooLong,
	agentaction.ValidationLocalAgentInvalid:         i18n.FieldAssistantLocalAgentInvalid,
	agentaction.ValidationLocalAgentUnavailable:     i18n.FieldAssistantLocalAgentUnavailable,
	agentaction.ValidationMemoryNameRequired:        i18n.FieldMemoryNameRequired,
	agentaction.ValidationMemoryNameTooLong:         i18n.FieldMemoryNameTooLong,
	agentaction.ValidationMemoryDescriptionRequired: i18n.FieldMemoryDescriptionRequired,
	agentaction.ValidationMemoryDescriptionTooLong:  i18n.FieldMemoryDescriptionTooLong,
	agentaction.ValidationMemoryBodyRequired:        i18n.FieldMemoryBodyRequired,
	agentaction.ValidationMemoryBodyTooLong:         i18n.FieldMemoryBodyTooLong,
	agentaction.ValidationStatusInvalid:             i18n.FieldUserStatusInvalid,
}

// ListAssistants 返回当前成员名下的助理。
func (o *directOperations) ListAssistants(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AssistantList, error) {
	return o.listOwnedAssistants(ctx, meta, identity, identity.User.ID)
}

// ListMemberAssistants 返回指定成员名下的助理。
func (o *directOperations) ListMemberAssistants(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.AssistantList, error) {
	return o.listOwnedAssistants(ctx, meta, identity, userID)
}

// listOwnedAssistants 读取指定成员名下的助理并解析头像地址。
func (o *directOperations) listOwnedAssistants(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.AssistantList, error) {
	records, err := o.listAssistants.Execute(ctx, identity, userID)
	if err != nil {
		return appservice.AssistantList{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantListFailed, identity.Organization.ID, "")
	}
	avatarFileIDs := make([]*string, 0, len(records))
	for _, record := range records {
		avatarFileIDs = append(avatarFileIDs, record.AvatarFileID)
	}
	avatarURLs, err := o.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.AssistantList{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantListFailed, identity.Organization.ID, "")
	}
	now := time.Now()
	assistants := make([]appservice.Assistant, 0, len(records))
	for _, record := range records {
		assistants = append(assistants, assistantFromAction(record, optionalFileURL(avatarURLs, record.AvatarFileID), now))
	}
	return appservice.AssistantList{Assistants: assistants}, nil
}

// GetAssistant 返回当前成员名下的助理详情。
func (o *directOperations) GetAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.AssistantDetail, error) {
	record, execution, err := o.getAssistant.Execute(ctx, identity, assistantID)
	if err != nil {
		return appservice.AssistantDetail{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantReadFailed, identity.Organization.ID, assistantID)
	}
	assistant, err := o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantReadFailed)
	if err != nil {
		return appservice.AssistantDetail{}, err
	}
	// 转换助理的完整执行配置契约。
	output := appservice.AgentExecution{MCPServerIDs: execution.MCPServerIDs, RevisionID: execution.RevisionID, Mode: appservice.AgentExecutionMode(execution.Mode)}
	if execution.Managed != nil {
		output.Managed = &appservice.AgentManagedExecution{
			ProviderID: execution.Managed.ProviderID, ProviderName: execution.Managed.ProviderName,
			ModelIdentifier: execution.Managed.ModelIdentifier, ModelName: execution.Managed.ModelName,
			SystemInstruction: execution.Managed.SystemInstruction, KnowledgeBaseIDs: execution.Managed.KnowledgeBaseIDs,
		}
	}
	if execution.LocalAgent != nil {
		output.LocalAgent = &appservice.AgentLocalAgentExecution{
			Kind: appservice.LocalAgentKind(execution.LocalAgent.Kind), SystemInstruction: execution.LocalAgent.SystemInstruction,
		}
	}
	return appservice.AssistantDetail{Assistant: assistant, Execution: output}, nil
}

// CreateAssistant 在当前成员的电脑上创建助理。
func (o *directOperations) CreateAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreateAssistantInput) (appservice.Assistant, error) {
	record, err := o.createAssistant.Execute(ctx, identity, input.DeviceID, agentaction.AssistantInput{
		DisplayName: input.DisplayName, AvatarFileID: input.AvatarFileID, Execution: assistantExecutionInput(input.Execution),
		MCPServerIDs: input.MCPServerIDs,
	})
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantCreateFailed, identity.Organization.ID, "")
	}
	return o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantCreateFailed)
}

// UpdateAssistant 修改当前成员名下的助理。
func (o *directOperations) UpdateAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string, input appservice.AssistantInput) (appservice.Assistant, error) {
	record, err := o.updateAssistant.Execute(ctx, identity, assistantID, agentaction.AssistantInput{
		DisplayName: input.DisplayName, AvatarFileID: input.AvatarFileID, Execution: assistantExecutionInput(input.Execution),
		MCPServerIDs: input.MCPServerIDs,
	})
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantUpdateFailed, identity.Organization.ID, assistantID)
	}
	slog.Info("助理已保存", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "revision_id", record.Execution.RevisionID)
	return o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantUpdateFailed)
}

// PauseAssistant 暂停当前成员名下的助理。
func (o *directOperations) PauseAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.Assistant, error) {
	return o.changeAssistantPaused(ctx, meta, identity, assistantID, true)
}

// ResumeAssistant 恢复当前成员名下已暂停的助理。
func (o *directOperations) ResumeAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.Assistant, error) {
	return o.changeAssistantPaused(ctx, meta, identity, assistantID, false)
}

// changeAssistantPaused 修改助理的暂停状态。
func (o *directOperations) changeAssistantPaused(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string, paused bool) (appservice.Assistant, error) {
	record, err := o.setAssistantPaused.Execute(ctx, identity, assistantID, paused)
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantPauseFailed, identity.Organization.ID, assistantID)
	}
	slog.Info("助理暂停状态已修改", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "paused", paused)
	return o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantPauseFailed)
}

// MoveAssistant 把当前成员名下的助理换到指定电脑。
func (o *directOperations) MoveAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string, input appservice.AssistantDeviceInput) (appservice.Assistant, error) {
	record, err := o.moveAssistant.Execute(ctx, identity, assistantID, input.DeviceID)
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantMoveFailed, identity.Organization.ID, assistantID)
	}
	return o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantMoveFailed)
}

// DeactivateAssistant 停用助理。
func (o *directOperations) DeactivateAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.Assistant, error) {
	return o.changeAssistantStatus(ctx, meta, identity, assistantID, domain.IdentityStatusInactive)
}

// ReactivateAssistant 启用已停用的助理。
func (o *directOperations) ReactivateAssistant(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.Assistant, error) {
	return o.changeAssistantStatus(ctx, meta, identity, assistantID, domain.IdentityStatusActive)
}

// changeAssistantStatus 修改助理的账号状态。
func (o *directOperations) changeAssistantStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string, status domain.IdentityStatus) (appservice.Assistant, error) {
	record, err := o.updateAssistantStatus.Execute(ctx, identity, assistantID, status)
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, i18n.ErrorAssistantStatusUpdateFailed, identity.Organization.ID, assistantID)
	}
	slog.Info("助理状态已修改", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "status", status)
	return o.assistantWithAvatar(ctx, meta, identity, record, i18n.ErrorAssistantStatusUpdateFailed)
}

// assistantWithAvatar 解析助理头像地址并转换契约。
func (o *directOperations) assistantWithAvatar(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, record *agentaction.Assistant, failureKey i18n.Key) (appservice.Assistant, error) {
	avatarURLs, err := o.optionalFileURLs(ctx, identity, record.AvatarFileID)
	if err != nil {
		return appservice.Assistant{}, o.assistantError(ctx, meta, err, failureKey, identity.Organization.ID, record.ID)
	}
	return assistantFromAction(*record, optionalFileURL(avatarURLs, record.AvatarFileID), time.Now()), nil
}

// ListAssistantMemories 返回当前成员名下助理的记忆，按最近更新排列。
func (o *directOperations) ListAssistantMemories(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID string) (appservice.AssistantMemoryList, error) {
	records, err := o.listMemories.Execute(ctx, identity, assistantID)
	if err != nil {
		return appservice.AssistantMemoryList{}, o.assistantError(ctx, meta, err, i18n.ErrorMemoryListFailed, identity.Organization.ID, assistantID)
	}
	memories := make([]appservice.AssistantMemory, 0, len(records))
	for _, record := range records {
		memories = append(memories, assistantMemoryFromAction(record))
	}
	return appservice.AssistantMemoryList{Memories: memories}, nil
}

// UpdateAssistantMemory 修改当前成员名下助理的一条记忆。
func (o *directOperations) UpdateAssistantMemory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID, memoryID string, input appservice.AssistantMemoryInput) (appservice.AssistantMemory, error) {
	record, err := o.updateMemory.Execute(ctx, identity, assistantID, memoryID, agentaction.AssistantMemoryInput{
		Name: input.Name, Description: input.Description, Body: input.Body,
	})
	if err != nil {
		return appservice.AssistantMemory{}, o.assistantError(ctx, meta, err, i18n.ErrorMemoryUpdateFailed, identity.Organization.ID, assistantID)
	}
	slog.Info("助理记忆已修改", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "memory_id", memoryID)
	return assistantMemoryFromAction(*record), nil
}

// DeleteAssistantMemory 删除当前成员名下助理的一条记忆。
func (o *directOperations) DeleteAssistantMemory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, assistantID, memoryID string) error {
	if err := o.deleteMemory.Execute(ctx, identity, assistantID, memoryID); err != nil {
		return o.assistantError(ctx, meta, err, i18n.ErrorMemoryDeleteFailed, identity.Organization.ID, assistantID)
	}
	slog.Info("助理记忆已删除", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "memory_id", memoryID)
	return nil
}

// assistantMemoryFromAction 转换助理记忆契约。
func assistantMemoryFromAction(record agentaction.AssistantMemory) appservice.AssistantMemory {
	return appservice.AssistantMemory{ID: record.ID, Name: record.Name, Description: record.Description, Body: record.Body, UpdatedAt: record.UpdatedAt}
}

// assistantError 转换助理操作错误。
func (o *directOperations) assistantError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, assistantID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, assistantFieldKeys))
	}
	if errors.Is(err, agentaction.ErrAssistantNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorAssistantNotFound)
	}
	if errors.Is(err, agentaction.ErrAssistantMemoryNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorMemoryNotFound)
	}
	if errors.Is(err, agentaction.ErrAssistantDeviceNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorDeviceNotFound)
	}
	if errors.Is(err, agentaction.ErrAssistantOwnerInactive) {
		return appservice.ConflictError(meta, i18n.ErrorAssistantOwnerInactive, "assistant_owner_inactive")
	}
	if errors.Is(err, fileaction.ErrLinkedImageNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	slog.Warn("助理操作失败", "organization_id", organizationID, "assistant_id", assistantID, "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
}

// assistantExecutionInput 转换助理的执行配置输入。
func assistantExecutionInput(input appservice.AgentExecutionInput) agentaction.ExecutionInput {
	output := agentaction.ExecutionInput{Mode: domain.AgentExecutionMode(input.Mode)}
	if input.Managed != nil {
		output.Managed = &agentaction.ManagedExecutionInput{
			ProviderID: input.Managed.ProviderID, ModelIdentifier: input.Managed.ModelIdentifier,
			SystemInstruction: input.Managed.SystemInstruction, KnowledgeBaseIDs: input.Managed.KnowledgeBaseIDs,
		}
	}
	if input.LocalAgent != nil {
		output.LocalAgent = &agentaction.LocalAgentExecutionInput{
			Kind: domain.LocalAgentKind(input.LocalAgent.Kind), SystemInstruction: input.LocalAgent.SystemInstruction,
		}
	}
	return output
}

// assistantFromAction 转换助理契约并按当前时间计算在线状态。
func assistantFromAction(record agentaction.Assistant, avatarURL string, now time.Time) appservice.Assistant {
	execution := appservice.AgentExecutionSummary{RevisionID: record.Execution.RevisionID, Mode: appservice.AgentExecutionMode(record.Execution.Mode)}
	if record.Execution.Managed != nil {
		execution.Managed = &appservice.AgentManagedExecutionSummary{
			ProviderID: record.Execution.Managed.ProviderID, ProviderName: record.Execution.Managed.ProviderName,
			ModelIdentifier: record.Execution.Managed.ModelIdentifier, ModelName: record.Execution.Managed.ModelName,
		}
	}
	if record.Execution.LocalAgent != nil {
		execution.LocalAgent = &appservice.AgentLocalAgentExecutionSummary{Kind: appservice.LocalAgentKind(record.Execution.LocalAgent.Kind)}
	}
	localAgents := make([]appservice.LocalAgentKind, 0, len(record.DeviceLocalAgents))
	for _, kind := range record.DeviceLocalAgents {
		localAgents = append(localAgents, appservice.LocalAgentKind(kind))
	}
	return appservice.Assistant{
		ID: record.ID, IdentityID: record.IdentityID, DisplayName: record.DisplayName, AvatarURL: avatarURL,
		Owner:     appservice.AssistantOwner{UserID: record.OwnerUserID, IdentityID: record.OwnerIdentityID, DisplayName: record.OwnerDisplayName},
		Device:    appservice.AssistantDevice{ID: record.DeviceID, Name: record.DeviceName, LocalAgents: localAgents},
		Status:    appservice.UserStatus(record.Status),
		Presence:  appservice.AssistantPresence(record.Presence(now)),
		Execution: execution,
		CreatedAt: record.CreatedAt,
	}
}
