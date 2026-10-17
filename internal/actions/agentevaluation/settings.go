//go:build server

package agentevaluation

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ValidationDecisionModelInvalid 表示评测判断模型不可用。
const ValidationDecisionModelInvalid common.FieldCode = "EVALUATION_DECISION_MODEL_INVALID"

// SettingsQuery 读取工作区独立的评测模型配置。
type SettingsQuery struct{ db *bun.DB }

// NewSettingsQuery 创建评测设置查询。
func NewSettingsQuery(db *bun.DB) *SettingsQuery { return &SettingsQuery{db: db} }

// Execute 返回已配置的评测判断模型编号。
func (q *SettingsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (*string, error) {
	setting := &servermodels.WorkspaceEvaluationSetting{}
	if err := q.db.NewSelect().Model(setting).Where("wes.workspace_id = ?", identity.Workspace.ID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("load evaluation settings: %w", err)
	}
	return setting.DecisionModelID, nil
}

// UpdateSettingsAction 修改评测判断模型配置。
type UpdateSettingsAction struct{ db *bun.DB }

// NewUpdateSettingsAction 创建评测设置修改操作。
func NewUpdateSettingsAction(db *bun.DB) *UpdateSettingsAction { return &UpdateSettingsAction{db: db} }

// Execute 校验模型用途并保存工作区的评测配置。
func (a *UpdateSettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, modelID *string) (*string, error) {
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if modelID != nil {
			if _, err := aimodel.Lock(ctx, tx, identity.Workspace.ID, *modelID, domain.AIModelUsageDecision); err != nil {
				if errors.Is(err, aimodel.ErrUnavailable) {
					return &common.FieldError{Fields: map[string]common.FieldCode{"decisionModelId": ValidationDecisionModelInvalid}}
				}
				return err
			}
		}
		setting := &servermodels.WorkspaceEvaluationSetting{WorkspaceID: identity.Workspace.ID, DecisionModelID: modelID}
		result, err := tx.NewUpdate().Model(setting).Column("decision_model_id").WherePK().Exec(ctx)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil {
			return err
		} else if count != 1 {
			return fmt.Errorf("update evaluation settings affected %d rows", count)
		}
		return nil
	})
	return modelID, err
}
