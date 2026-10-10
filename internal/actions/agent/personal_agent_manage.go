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

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// CreatePersonalAgentAction 由成员在自己的电脑上创建个人 AI 员工。
type CreatePersonalAgentAction struct{ db *bun.DB }

// NewCreatePersonalAgentAction 创建个人 AI 员工新增操作。
func NewCreatePersonalAgentAction(db *bun.DB) *CreatePersonalAgentAction {
	return &CreatePersonalAgentAction{db: db}
}

// Execute 创建绑定当前成员指定电脑的个人 AI 员工及其首个执行配置版本。
func (a *CreatePersonalAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, computerID string, input PersonalAgentInput) (*PersonalAgent, error) {
	input, execution, err := normalizePersonalAgentInput(input)
	if err != nil {
		return nil, err
	}
	var agentID string
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 校验并锁定授权的业务系统。
		businessSystems, err := validateAndLockBusinessSystems(ctx, tx, identity.Workspace.ID, input.BusinessSystems)
		if err != nil {
			return err
		}
		model, err := lockPersonalAgentModel(ctx, tx, identity.Workspace.ID, execution)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Workspace.ID, execution); err != nil {
			return err
		}
		if err := lockOwnActiveComputer(ctx, tx, identity, computerID); err != nil {
			return err
		}
		workspaceIdentity := &servermodels.WorkspaceIdentity{
			WorkspaceID: identity.Workspace.ID, Type: string(domain.WorkspaceIdentityTypeAgent),
			DisplayName: input.DisplayName, WorkStatus: string(domain.WorkStatusWorking),
		}
		if input.AvatarFileID != "" {
			if workspaceIdentity.AvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, nil); err != nil {
				return err
			}
		}
		if _, err := tx.NewInsert().Model(workspaceIdentity).
			Column("workspace_id", "type", "display_name", "avatar_file_id", "work_status").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		revisionID := uuid.NewV7().String()
		stored := &servermodels.Agent{
			IdentityID: workspaceIdentity.ID, WorkspaceID: identity.Workspace.ID, ActiveRevisionID: revisionID,
			Status: string(domain.IdentityStatusActive), ResponsibleUserID: &identity.User.ID, ComputerID: &computerID,
			ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudiencePersonal}, LocalAgents: input.LocalAgents,
		}
		if _, err := tx.NewInsert().Model(stored).
			Column("identity_id", "workspace_id", "active_revision_id", "status", "responsible_user_id", "computer_id", "service_audiences", "local_agents").
			Returning("id").Exec(ctx); err != nil {
			return err
		}
		agentID = stored.ID
		_, err = insertExecutionRevision(ctx, tx, identity, stored.ID, revisionID, execution, model, businessSystems)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create personal agent: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "个人 AI 员工已创建", "agent_id", agentID, "computer_id", computerID)
	return loadPersonalAgent(ctx, a.db, identity.Workspace.ID, agentID, identity.User.ID)
}

// UpdatePersonalAgentAction 由负责人修改个人 AI 员工的资料与执行配置。
type UpdatePersonalAgentAction struct{ db *bun.DB }

// NewUpdatePersonalAgentAction 创建个人 AI 员工修改操作。
func NewUpdatePersonalAgentAction(db *bun.DB) *UpdatePersonalAgentAction {
	return &UpdatePersonalAgentAction{db: db}
}

// Execute 保存个人 AI 员工名称与头像，并生成新的执行配置版本。
func (a *UpdatePersonalAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, input PersonalAgentInput) (*PersonalAgent, error) {
	input, execution, err := normalizePersonalAgentInput(input)
	if err != nil {
		return nil, err
	}
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 按业务系统、模型、知识库、个人 AI 员工的顺序取锁。
		businessSystems, err := validateAndLockBusinessSystems(ctx, tx, identity.Workspace.ID, input.BusinessSystems)
		if err != nil {
			return err
		}
		model, err := lockPersonalAgentModel(ctx, tx, identity.Workspace.ID, execution)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Workspace.ID, execution); err != nil {
			return err
		}
		stored, err := lockOwnPersonalAgent(ctx, tx, identity, agentID)
		if err != nil {
			return err
		}
		var currentAvatarFileID *string
		if err := tx.NewSelect().TableExpr("workspace_identities AS oi").ColumnExpr("oi.avatar_file_id::text").
			Where("oi.workspace_id = ? AND oi.id = ?", identity.Workspace.ID, stored.IdentityID).
			For("UPDATE OF oi").Scan(ctx, &currentAvatarFileID); err != nil {
			return fmt.Errorf("lock personal agent identity: %w", err)
		}
		// 传入新头像时激活该图片，替换下来的旧头像交给清理任务。
		var nextAvatarFileID *string
		if input.AvatarFileID != "" {
			if nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeAgentAvatar, input.AvatarFileID, currentAvatarFileID); err != nil {
				return err
			}
			if err := fileaction.RetireLinkedImage(ctx, tx, identity.Workspace.ID, currentAvatarFileID, nextAvatarFileID); err != nil {
				return err
			}
		}
		var displayChanged bool
		if err := tx.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("avatar_file_id = COALESCE(?, avatar_file_id)", nextAvatarFileID).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, stored.IdentityID).
			Returning("(old.display_name, old.avatar_file_id) IS DISTINCT FROM (new.display_name, new.avatar_file_id)").
			Scan(ctx, &displayChanged); err != nil {
			return err
		}
		revisionID := uuid.NewV7().String()
		if _, err := insertExecutionRevision(ctx, tx, identity, stored.ID, revisionID, execution, model, businessSystems); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("active_revision_id = ?", revisionID).Set("local_agents = ?", localAgentsJSON(input.LocalAgents)).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		if err := releaseStaleLocalAgentSessions(ctx, tx, identity.Workspace.ID, stored.ID, stored.ComputerID, input.LocalAgents); err != nil {
			return err
		}
		if !displayChanged {
			return nil
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("update personal agent: %w", err)
	}
	return loadPersonalAgent(ctx, a.db, identity.Workspace.ID, agentID, identity.User.ID)
}

// SetPersonalAgentPausedAction 由负责人暂停或恢复个人 AI 员工。
type SetPersonalAgentPausedAction struct{ db *bun.DB }

// NewSetPersonalAgentPausedAction 创建个人 AI 员工暂停与恢复操作。
func NewSetPersonalAgentPausedAction(db *bun.DB) *SetPersonalAgentPausedAction {
	return &SetPersonalAgentPausedAction{db: db}
}

// Execute 暂停时拒绝之后的新触发，已派发的运行不受影响；状态实际变化时推进展示该个人 AI 员工的会话版本。
func (a *SetPersonalAgentPausedAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, paused bool) (*PersonalAgent, error) {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored, err := lockOwnPersonalAgent(ctx, tx, identity, agentID)
		if err != nil {
			return err
		}
		if (stored.PausedAt != nil) == paused {
			return nil
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("paused_at = CASE WHEN ? THEN now() END", paused).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("set personal agent paused: %w", err)
	}
	return loadPersonalAgent(ctx, a.db, identity.Workspace.ID, agentID, identity.User.ID)
}

// MovePersonalAgentAction 由负责人把个人 AI 员工换到自己的另一台电脑。
type MovePersonalAgentAction struct{ db *bun.DB }

// NewMovePersonalAgentAction 创建个人 AI 员工换电脑操作。
func NewMovePersonalAgentAction(db *bun.DB) *MovePersonalAgentAction {
	return &MovePersonalAgentAction{db: db}
}

// Execute 把个人 AI 员工绑定到当前成员名下的指定电脑；原电脑上尚未领取的运行由收敛扫描结束。
func (a *MovePersonalAgentAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, computerID string) (*PersonalAgent, error) {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored, err := lockOwnPersonalAgent(ctx, tx, identity, agentID)
		if err != nil {
			return err
		}
		if err := lockOwnActiveComputer(ctx, tx, identity, computerID); err != nil {
			return err
		}
		if stored.ComputerID != nil && *stored.ComputerID == computerID {
			return nil
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("computer_id = ?", computerID).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, stored.ID).
			Exec(ctx); err != nil {
			return err
		}
		if err := releaseStaleLocalAgentSessions(ctx, tx, identity.Workspace.ID, stored.ID, &computerID, stored.LocalAgents); err != nil {
			return err
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, stored.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("move personal agent: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "个人 AI 员工已换电脑", "agent_id", agentID, "computer_id", computerID)
	return loadPersonalAgent(ctx, a.db, identity.Workspace.ID, agentID, identity.User.ID)
}

// UpdatePersonalAgentStatusAction 停用或启用个人 AI 员工。
type UpdatePersonalAgentStatusAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewUpdatePersonalAgentStatusAction 创建个人 AI 员工状态修改操作。
func NewUpdatePersonalAgentStatusAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *UpdatePersonalAgentStatusAction {
	return &UpdatePersonalAgentStatusAction{db: db, enqueuer: enqueuer}
}

// Execute 停用或启用当前企业的个人 AI 员工，停用时取消其等待确认或审批的操作；负责人已停用时不能启用；操作他人负责的个人 AI 员工需要工作区管理权限。
func (a *UpdatePersonalAgentStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (*PersonalAgent, error) {
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"status": ValidationStatusInvalid}}
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 只锁个人 AI 员工记录，锁定后读取负责人最新状态；停用成员时会锁定并停用其负责的全部个人 AI 员工，与此处先后提交都不会留下负责人已停用而个人 AI 员工启用的结果。
		var locked struct {
			IdentityID        string                `bun:"identity_id"`
			Status            domain.IdentityStatus `bun:"status"`
			ResponsibleUserID string                `bun:"responsible_user_id"`
		}
		if err := tx.NewSelect().Model((*servermodels.Agent)(nil)).
			ColumnExpr("a.identity_id::text AS identity_id, a.status, a.responsible_user_id::text AS responsible_user_id").
			Where(personalAgentCondition).
			Where("a.workspace_id = ? AND a.id = ?", identity.Workspace.ID, agentID).
			For("UPDATE OF a").
			Scan(ctx, &locked); errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		} else if err != nil {
			return fmt.Errorf("lock personal agent: %w", err)
		}
		if locked.ResponsibleUserID != identity.User.ID && !identity.HasPermission(domain.PermissionWorkspaceManage) {
			return ErrPersonalAgentForbidden
		}
		var responsibleStatus domain.IdentityStatus
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("status").
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, locked.ResponsibleUserID).
			Scan(ctx, &responsibleStatus); err != nil {
			return fmt.Errorf("load personal agent responsible status: %w", err)
		}
		if locked.Status == status {
			return nil
		}
		if status == domain.IdentityStatusActive && responsibleStatus != domain.IdentityStatusActive {
			return ErrPersonalAgentResponsibleInactive
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("status = ?", status).
			Where("workspace_id = ? AND id = ?", identity.Workspace.ID, agentID).
			Exec(ctx); err != nil {
			return err
		}
		if status == domain.IdentityStatusInactive {
			if err := agentprocess.CancelAgentToolDecisions(ctx, tx, a.enqueuer, identity.Workspace.ID, locked.IdentityID); err != nil {
				return err
			}
		}
		return chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, locked.IdentityID)
	})
	if err != nil {
		return nil, fmt.Errorf("update personal agent status: %w", err)
	}
	return loadPersonalAgent(ctx, a.db, identity.Workspace.ID, agentID, "")
}

// ListPersonalAgentsQuery 读取成员负责的个人 AI 员工。
type ListPersonalAgentsQuery struct{ db *bun.DB }

// NewListPersonalAgentsQuery 创建个人 AI 员工列表查询。
func NewListPersonalAgentsQuery(db *bun.DB) *ListPersonalAgentsQuery {
	return &ListPersonalAgentsQuery{db: db}
}

// Execute 返回指定成员负责的按名称排列的个人 AI 员工。
func (q *ListPersonalAgentsQuery) Execute(ctx context.Context, identity *servermodels.Identity, responsibleUserID string) ([]PersonalAgent, error) {
	personalAgents := make([]PersonalAgent, 0)
	if err := personalAgentQuery(q.db, identity.Workspace.ID).
		Where("a.responsible_user_id = ?", responsibleUserID).
		OrderExpr("lower(oi.display_name) ASC, a.id ASC").
		Scan(ctx, &personalAgents); err != nil {
		return nil, fmt.Errorf("list personal agents: %w", err)
	}
	ids := arr.Map(personalAgents, func(personalAgent PersonalAgent) string { return personalAgent.ID })
	summaries, err := loadAgentExecutionSummaries(ctx, q.db, identity.Workspace.ID, ids)
	if err != nil {
		return nil, fmt.Errorf("load personal agent execution summaries: %w", err)
	}
	for index := range personalAgents {
		personalAgents[index].Execution = summaries[personalAgents[index].ID]
	}
	return personalAgents, nil
}

// GetPersonalAgentQuery 读取当前成员负责的个人 AI 员工详情与完整执行配置。
type GetPersonalAgentQuery struct{ db *bun.DB }

// NewGetPersonalAgentQuery 创建个人 AI 员工详情查询。
func NewGetPersonalAgentQuery(db *bun.DB) *GetPersonalAgentQuery {
	return &GetPersonalAgentQuery{db: db}
}

// Execute 返回当前成员负责的个人 AI 员工及其当前执行配置。
func (q *GetPersonalAgentQuery) Execute(ctx context.Context, identity *servermodels.Identity, agentID string) (*PersonalAgent, Execution, error) {
	personalAgent, err := loadPersonalAgent(ctx, q.db, identity.Workspace.ID, agentID, identity.User.ID)
	if err != nil {
		return nil, Execution{}, err
	}
	execution, err := loadAgentExecution(ctx, q.db, identity.Workspace.ID, personalAgent.ID)
	if err != nil {
		return nil, Execution{}, fmt.Errorf("load personal agent execution: %w", err)
	}
	return personalAgent, execution, nil
}

// normalizePersonalAgentInput 规范化并校验个人 AI 员工名称与执行配置。
func normalizePersonalAgentInput(input PersonalAgentInput) (PersonalAgentInput, ExecutionInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return input, ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameRequired}}
	}
	if !domain.IdentityDisplayNameValid(input.DisplayName) {
		return input, ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"displayName": ValidationDisplayNameInvalid}}
	}
	execution, err := normalizeExecutionInput(input.Execution)
	if err != nil {
		return input, ExecutionInput{}, err
	}
	input.LocalAgents = NormalizeLocalAgents(input.LocalAgents)
	return input, execution, nil
}

// lockPersonalAgentModel 校验并锁定个人 AI 员工所用模型。
func lockPersonalAgentModel(ctx context.Context, tx bun.Tx, workspaceID string, execution ExecutionInput) (aimodel.Option, error) {
	return lockManagedExecutionModel(ctx, tx, workspaceID, *execution.Managed)
}
