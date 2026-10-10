//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateExecutionInput 定义编辑页保存的完整执行配置。
type UpdateExecutionInput struct {
	ExecutionInput
	BusinessSystems []domain.BusinessSystemGrant
}

// UpdateExecutionAction 修改 AI 员工当前生效的执行配置。
type UpdateExecutionAction struct{ db *bun.DB }

// NewUpdateExecutionAction 创建 AI 员工执行配置修改操作。
func NewUpdateExecutionAction(db *bun.DB) *UpdateExecutionAction {
	return &UpdateExecutionAction{db: db}
}

// Execute 创建新执行配置版本并切换 AI 员工的当前版本。
func (a *UpdateExecutionAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, input UpdateExecutionInput) (*Agent, error) {
	executionInput, err := normalizeExecutionInput(input.ExecutionInput)
	if err != nil {
		return nil, err
	}
	var output *Agent
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 保存与删除均按业务系统、模型、知识库、员工的顺序取锁。
		businessSystems, err := validateAndLockBusinessSystems(ctx, tx, identity.Workspace.ID, input.BusinessSystems)
		if err != nil {
			return err
		}
		model, err := lockManagedExecutionModel(ctx, tx, identity.Workspace.ID, *executionInput.Managed)
		if err != nil {
			return err
		}
		if err := lockExecutionKnowledgeBases(ctx, tx, identity.Workspace.ID, executionInput); err != nil {
			return err
		}
		stored := &servermodels.Agent{}
		err = tx.NewSelect().Model(stored).
			Column("id", "responsible_user_id").
			Where("a.workspace_id = ?", identity.Workspace.ID).
			Where("a.id = ?", agentID).
			Where(serviceAgentCondition).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 需要审批的授权由负责人审批，AI 员工须已指定负责人。
		if stored.ResponsibleUserID == nil && slices.ContainsFunc(businessSystems, domain.BusinessSystemGrant.RequiresApproval) {
			return &common.FieldError{Fields: map[string]common.FieldCode{"businessSystems": ValidationResponsibleRequired}}
		}
		revisionID := uuid.NewV7()
		execution, err := insertExecutionRevision(ctx, tx, identity, agentID, revisionID.String(), executionInput, model, businessSystems)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("active_revision_id = ?", execution.RevisionID).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", agentID).
			Exec(ctx); err != nil {
			return err
		}
		output, err = loadAgent(ctx, tx, identity.Workspace.ID, agentID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update agent execution: %w", err)
	}
	return output, nil
}
