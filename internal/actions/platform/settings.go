//go:build server

package platform

import (
	"context"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Settings 定义平台管理员可修改的注册策略与工作区创建策略。
type Settings struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
}

// settingsFromModel 读取平台行中的平台级策略。
func settingsFromModel(platform *servermodels.Platform) Settings {
	return Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(platform.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(platform.WorkspaceCreationPolicy),
	}
}

// SettingsQuery 读取平台级策略。
type SettingsQuery struct {
	db *bun.DB
}

// NewSettingsQuery 创建平台级策略查询。
func NewSettingsQuery(db *bun.DB) *SettingsQuery {
	return &SettingsQuery{db: db}
}

// Execute 返回当前注册策略与工作区创建策略。
func (q *SettingsQuery) Execute(ctx context.Context) (Settings, error) {
	platform, err := Load(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	return settingsFromModel(platform), nil
}

// Policies 定义平台管理员可修改的注册策略和工作区创建策略。
type Policies struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
}

// UpdatePoliciesAction 修改注册策略和工作区创建策略。
type UpdatePoliciesAction struct {
	db    *bun.DB
	state DeploymentReloader
}

// NewUpdatePoliciesAction 创建平台策略修改操作，state 在保存后刷新。
func NewUpdatePoliciesAction(db *bun.DB, state DeploymentReloader) *UpdatePoliciesAction {
	return &UpdatePoliciesAction{db: db, state: state}
}

// Execute 由仍有效的平台管理员保存注册策略和工作区创建策略，并刷新本实例的部署状态；刷新失败时由下次心跳刷新。
func (a *UpdatePoliciesAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input Policies) (Settings, error) {
	settings, err := Update(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.RegistrationPolicy = string(input.RegistrationPolicy)
		platform.WorkspaceCreationPolicy = string(input.WorkspaceCreationPolicy)
		_, err := tx.NewUpdate().Model(platform).
			Column("registration_policy", "workspace_creation_policy").
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	slog.InfoContext(logscope.WithAccount(ctx, operator.Account.ID), "已保存平台策略", "registration_policy", input.RegistrationPolicy, "workspace_creation_policy", input.WorkspaceCreationPolicy)
	if err := a.state.Reload(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "保存平台策略后刷新部署状态失败", "error", err)
	}
	return settings, nil
}

// Update 在事务内确认操作者仍是有效平台管理员并锁定平台行，执行修改后返回最新的平台设置。
func Update(ctx context.Context, db *bun.DB, operator *servermodels.AccountIdentity, update func(context.Context, bun.Tx, *servermodels.Platform) error) (Settings, error) {
	var output Settings
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := LockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		platform, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := update(ctx, tx, platform); err != nil {
			return err
		}
		output = settingsFromModel(platform)
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return output, nil
}
