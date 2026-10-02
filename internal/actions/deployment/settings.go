//go:build server

package deployment

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Settings 定义部署管理员可修改的部署级策略。
type Settings struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
}

// settingsFromModel 读取部署实例行中的部署级策略。
func settingsFromModel(deployment *servermodels.Deployment) Settings {
	return Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(deployment.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(deployment.WorkspaceCreationPolicy),
	}
}

// SettingsQuery 读取部署级策略。
type SettingsQuery struct {
	db *bun.DB
}

// NewSettingsQuery 创建部署级策略查询。
func NewSettingsQuery(db *bun.DB) *SettingsQuery {
	return &SettingsQuery{db: db}
}

// Execute 返回当前注册策略和工作区创建策略。
func (q *SettingsQuery) Execute(ctx context.Context) (Settings, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	return settingsFromModel(deployment), nil
}

// UpdateSettingsAction 修改部署级策略。
type UpdateSettingsAction struct {
	db *bun.DB
}

// NewUpdateSettingsAction 创建部署级策略修改操作。
func NewUpdateSettingsAction(db *bun.DB) *UpdateSettingsAction {
	return &UpdateSettingsAction{db: db}
}

// Execute 校验策略取值后，由仍有效的部署管理员保存注册策略和工作区创建策略。
func (a *UpdateSettingsAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input Settings) (Settings, error) {
	fields := map[string]common.FieldCode{}
	if !input.RegistrationPolicy.Valid() {
		fields["registrationPolicy"] = ValidationRegistrationPolicyInvalid
	}
	if !input.WorkspaceCreationPolicy.Valid() {
		fields["workspaceCreationPolicy"] = ValidationWorkspaceCreationPolicyInvalid
	}
	if len(fields) > 0 {
		return Settings{}, &common.FieldError{Fields: fields}
	}
	var output Settings
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		deployment, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		deployment.RegistrationPolicy = string(input.RegistrationPolicy)
		deployment.WorkspaceCreationPolicy = string(input.WorkspaceCreationPolicy)
		if _, err := tx.NewUpdate().Model(deployment).
			Column("registration_policy", "workspace_creation_policy").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		output = settingsFromModel(deployment)
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return output, nil
}
