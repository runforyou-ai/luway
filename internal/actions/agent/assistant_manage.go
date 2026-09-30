//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateAssistantAction 由成员在自己的电脑上创建助理。
type CreateAssistantAction struct{ db *bun.DB }

// NewCreateAssistantAction 创建助理新增操作。
func NewCreateAssistantAction(db *bun.DB) *CreateAssistantAction {
	return &CreateAssistantAction{db: db}
}

// Execute 创建绑定当前成员指定电脑的助理及其首个执行配置版本。
func (a *CreateAssistantAction) Execute(ctx context.Context, identity *servermodels.Identity, deviceID string, input AssistantInput) (*Assistant, error) {
	input, execution, err := normalizeAssistantInput(input)
	if err != nil {
		return nil, err
	}
	var assistantID string
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 助理不接待客户，只能使用不按客户查询的企业服务。
		mcpServerIDs, err := validateAndLockMCPServers(ctx, tx, identity.Organization.ID, input.MCPServerIDs, false)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Organization.ID, execution); err != nil {
			return err
		}
		if err := lockOwnActiveDevice(ctx, tx, identity, deviceID); err != nil {
			return err
		}
		model, err := prepareAssistantExecution(ctx, tx, identity.Organization.ID, deviceID, execution)
		if err != nil {
			return err
		}
		organizationIdentity := &servermodels.OrganizationIdentity{
			OrganizationID: identity.Organization.ID, Type: string(domain.OrganizationIdentityTypeAssistant),
			DisplayName: input.DisplayName, WorkStatus: string(domain.WorkStatusWorking),
		}
		if input.AvatarFileID != "" {
			if organizationIdentity.AvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, nil); err != nil {
				return err
			}
		}
		if _, err := tx.NewInsert().Model(organizationIdentity).
			Column("organization_id", "type", "display_name", "avatar_file_id", "work_status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		revisionID := uuid.NewV7().String()
		stored := &servermodels.Agent{
			IdentityID: organizationIdentity.ID, OrganizationID: identity.Organization.ID, ActiveRevisionID: revisionID,
			Status: string(domain.IdentityStatusActive), OwnerUserID: &identity.User.ID, DeviceID: &deviceID,
		}
		if _, err := tx.NewInsert().Model(stored).
			Column("identity_id", "organization_id", "active_revision_id", "status", "owner_user_id", "device_id").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		assistantID = stored.ID
		_, err = insertExecutionRevision(ctx, tx, identity, stored.ID, revisionID, execution, model, mcpServerIDs)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create assistant: %w", err)
	}
	slog.Info("助理已创建", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "device_id", deviceID)
	return loadAssistant(ctx, a.db, identity.Organization.ID, assistantID, identity.User.ID)
}

// UpdateAssistantAction 由主人修改助理的资料与执行配置。
type UpdateAssistantAction struct{ db *bun.DB }

// NewUpdateAssistantAction 创建助理修改操作。
func NewUpdateAssistantAction(db *bun.DB) *UpdateAssistantAction {
	return &UpdateAssistantAction{db: db}
}

// Execute 保存助理名称与头像，并生成新的执行配置版本。
func (a *UpdateAssistantAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID string, input AssistantInput) (*Assistant, error) {
	input, execution, err := normalizeAssistantInput(input)
	if err != nil {
		return nil, err
	}
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 助理不接待客户，只能使用不按客户查询的企业服务；按服务、知识库、助理的顺序取锁。
		mcpServerIDs, err := validateAndLockMCPServers(ctx, tx, identity.Organization.ID, input.MCPServerIDs, false)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Organization.ID, execution); err != nil {
			return err
		}
		stored, err := lockOwnAssistant(ctx, tx, identity, assistantID)
		if err != nil {
			return err
		}
		model, err := prepareAssistantExecution(ctx, tx, identity.Organization.ID, *stored.DeviceID, execution)
		if err != nil {
			return err
		}
		var currentAvatarFileID *string
		if err := tx.NewSelect().TableExpr("organization_identities AS oi").ColumnExpr("oi.avatar_file_id::text").
			Where("oi.organization_id = ? AND oi.id = ?", identity.Organization.ID, stored.IdentityID).
			For("UPDATE OF oi").Scan(ctx, &currentAvatarFileID); err != nil {
			return fmt.Errorf("lock assistant identity: %w", err)
		}
		// 传入新头像时激活该图片，替换下来的旧头像交给清理任务。
		var nextAvatarFileID *string
		if input.AvatarFileID != "" {
			if nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, currentAvatarFileID); err != nil {
				return err
			}
			if err := fileaction.RetireLinkedImage(ctx, tx, identity.Organization.ID, currentAvatarFileID, nextAvatarFileID); err != nil {
				return err
			}
		}
		var displayChanged bool
		if err := tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("avatar_file_id = COALESCE(?, avatar_file_id)", nextAvatarFileID).
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, stored.IdentityID).
			Returning("(old.display_name, old.avatar_file_id) IS DISTINCT FROM (new.display_name, new.avatar_file_id)").
			Scan(ctx, &displayChanged); err != nil {
			return err
		}
		revisionID := uuid.NewV7().String()
		if _, err := insertExecutionRevision(ctx, tx, identity, stored.ID, revisionID, execution, model, mcpServerIDs); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("active_revision_id = ?", revisionID).Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		if !displayChanged {
			return nil
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("update assistant: %w", err)
	}
	return loadAssistant(ctx, a.db, identity.Organization.ID, assistantID, identity.User.ID)
}

// SetAssistantPausedAction 由主人暂停或恢复助理。
type SetAssistantPausedAction struct{ db *bun.DB }

// NewSetAssistantPausedAction 创建助理暂停与恢复操作。
func NewSetAssistantPausedAction(db *bun.DB) *SetAssistantPausedAction {
	return &SetAssistantPausedAction{db: db}
}

// Execute 暂停时拒绝之后的新触发，已派发的运行不受影响；状态实际变化时推进展示该助理的会话版本。
func (a *SetAssistantPausedAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID string, paused bool) (*Assistant, error) {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored, err := lockOwnAssistant(ctx, tx, identity, assistantID)
		if err != nil {
			return err
		}
		if (stored.PausedAt != nil) == paused {
			return nil
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("paused_at = CASE WHEN ? THEN now() END", paused).Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("set assistant paused: %w", err)
	}
	return loadAssistant(ctx, a.db, identity.Organization.ID, assistantID, identity.User.ID)
}

// MoveAssistantAction 由主人把助理换到自己的另一台电脑。
type MoveAssistantAction struct{ db *bun.DB }

// NewMoveAssistantAction 创建助理换电脑操作。
func NewMoveAssistantAction(db *bun.DB) *MoveAssistantAction {
	return &MoveAssistantAction{db: db}
}

// Execute 把助理绑定到当前成员名下的指定电脑；原电脑上尚未领取的运行由收敛扫描结束。
func (a *MoveAssistantAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID, deviceID string) (*Assistant, error) {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored, err := lockOwnAssistant(ctx, tx, identity, assistantID)
		if err != nil {
			return err
		}
		if err := lockOwnActiveDevice(ctx, tx, identity, deviceID); err != nil {
			return err
		}
		if stored.DeviceID != nil && *stored.DeviceID == deviceID {
			return nil
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("device_id = ?", deviceID).Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("move assistant: %w", err)
	}
	slog.Info("助理已换绑电脑", "organization_id", identity.Organization.ID, "assistant_id", assistantID, "device_id", deviceID)
	return loadAssistant(ctx, a.db, identity.Organization.ID, assistantID, identity.User.ID)
}

// UpdateAssistantStatusAction 停用或启用助理，主人与管理员共用。
type UpdateAssistantStatusAction struct{ db *bun.DB }

// NewUpdateAssistantStatusAction 创建助理状态修改操作。
func NewUpdateAssistantStatusAction(db *bun.DB) *UpdateAssistantStatusAction {
	return &UpdateAssistantStatusAction{db: db}
}

// Execute 停用或启用当前企业的助理；主人已停用时不能启用。
func (a *UpdateAssistantStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, assistantID string, status domain.IdentityStatus) (*Assistant, error) {
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"status": ValidationStatusInvalid}}
	}
	if !common.ValidUUID(assistantID) {
		return nil, ErrAssistantNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 只锁助理记录，锁定后读取主人最新状态；停用成员时会锁定并停用其名下全部助理，与此处先后提交都不会留下主人已停用而助理启用的结果。
		var locked struct {
			IdentityID  string                `bun:"identity_id"`
			Status      domain.IdentityStatus `bun:"status"`
			OwnerUserID string                `bun:"owner_user_id"`
		}
		if err := tx.NewSelect().Model((*servermodels.Agent)(nil)).
			ColumnExpr("a.identity_id::text AS identity_id, a.status, a.owner_user_id::text AS owner_user_id").
			Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeAssistant).
			Where("a.organization_id = ? AND a.id = ?", identity.Organization.ID, assistantID).
			For("UPDATE OF a").
			Scan(ctx, &locked); errors.Is(err, sql.ErrNoRows) {
			return ErrAssistantNotFound
		} else if err != nil {
			return fmt.Errorf("lock assistant: %w", err)
		}
		var ownerStatus domain.IdentityStatus
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("status").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, locked.OwnerUserID).
			Scan(ctx, &ownerStatus); err != nil {
			return fmt.Errorf("load assistant owner status: %w", err)
		}
		if locked.Status == status {
			return nil
		}
		if status == domain.IdentityStatusActive && ownerStatus != domain.IdentityStatusActive {
			return ErrAssistantOwnerInactive
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("status = ?", status).Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, assistantID).
			Exec(ctx); err != nil {
			return err
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, locked.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("update assistant status: %w", err)
	}
	return loadAssistant(ctx, a.db, identity.Organization.ID, assistantID, "")
}

// ListAssistantsQuery 读取成员名下的助理。
type ListAssistantsQuery struct{ db *bun.DB }

// NewListAssistantsQuery 创建助理列表查询。
func NewListAssistantsQuery(db *bun.DB) *ListAssistantsQuery {
	return &ListAssistantsQuery{db: db}
}

// Execute 返回指定成员名下按名称排列的助理。
func (q *ListAssistantsQuery) Execute(ctx context.Context, identity *servermodels.Identity, ownerUserID string) ([]Assistant, error) {
	if !common.ValidUUID(ownerUserID) {
		return nil, ErrAssistantNotFound
	}
	assistants := make([]Assistant, 0)
	if err := assistantQuery(q.db, identity.Organization.ID).
		Where("a.owner_user_id = ?", ownerUserID).
		OrderExpr("lower(oi.display_name) ASC, a.id ASC").
		Scan(ctx, &assistants); err != nil {
		return nil, fmt.Errorf("list assistants: %w", err)
	}
	ids := make([]string, 0, len(assistants))
	for _, assistant := range assistants {
		ids = append(ids, assistant.ID)
	}
	summaries, err := loadAgentExecutionSummaries(ctx, q.db, identity.Organization.ID, ids)
	if err != nil {
		return nil, fmt.Errorf("load assistant execution summaries: %w", err)
	}
	for index := range assistants {
		assistants[index].Execution = summaries[assistants[index].ID]
	}
	return assistants, nil
}

// GetAssistantQuery 读取当前成员名下的助理详情与完整执行配置。
type GetAssistantQuery struct{ db *bun.DB }

// NewGetAssistantQuery 创建助理详情查询。
func NewGetAssistantQuery(db *bun.DB) *GetAssistantQuery {
	return &GetAssistantQuery{db: db}
}

// Execute 返回当前成员名下的助理及其当前执行配置。
func (q *GetAssistantQuery) Execute(ctx context.Context, identity *servermodels.Identity, assistantID string) (*Assistant, Execution, error) {
	assistant, err := loadAssistant(ctx, q.db, identity.Organization.ID, assistantID, identity.User.ID)
	if err != nil {
		return nil, Execution{}, err
	}
	execution, err := loadAgentExecution(ctx, q.db, identity.Organization.ID, assistant.ID)
	if err != nil {
		return nil, Execution{}, fmt.Errorf("load assistant execution: %w", err)
	}
	return assistant, execution, nil
}

// normalizeAssistantInput 规范化并校验助理名称与执行配置；本机 Agent 执行不使用企业 MCP 服务。
func normalizeAssistantInput(input AssistantInput) (AssistantInput, ExecutionInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return input, ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameRequired}}
	}
	if !domain.IdentityDisplayNameValid(input.DisplayName) {
		return input, ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameInvalid}}
	}
	execution, err := normalizeExecutionInput(input.Execution, true)
	if err != nil {
		return input, ExecutionInput{}, err
	}
	if execution.Mode == domain.AgentExecutionModeLocalAgent && len(input.MCPServerIDs) > 0 {
		return input, ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"mcpServerIds": ValidationMCPServerInvalid}}
	}
	return input, execution, nil
}

// prepareAssistantExecution 校验助理执行配置依赖的资源：平台托管执行返回所用模型，本机 Agent 执行要求绑定电脑已上报该本机 Agent 可用。
func prepareAssistantExecution(ctx context.Context, tx bun.Tx, organizationID, deviceID string, execution ExecutionInput) (ModelOption, error) {
	if execution.Mode == domain.AgentExecutionModeManaged {
		return loadManagedExecutionModel(ctx, tx, organizationID, *execution.Managed)
	}
	available, err := tx.NewSelect().Model((*servermodels.Device)(nil)).
		Where("d.organization_id = ? AND d.id = ?", organizationID, deviceID).
		Where("d.local_agents @> ?::jsonb", fmt.Sprintf(`[%q]`, execution.LocalAgent.Kind)).
		Exists(ctx)
	if err != nil {
		return ModelOption{}, fmt.Errorf("check assistant local agent: %w", err)
	}
	if !available {
		return ModelOption{}, &common.FieldError{Fields: map[string]common.FieldCode{"localAgent": ValidationLocalAgentUnavailable}}
	}
	return ModelOption{}, nil
}
