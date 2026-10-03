//go:build server

package deployment

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/timezone"
	"github.com/uptrace/bun"
)

// Settings 定义部署管理员可修改的部署级策略与运营数据统计时区。
type Settings struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
	StatisticsTimeZone      string
}

// settingsFromModel 读取部署实例行中的部署级策略。
func settingsFromModel(deployment *servermodels.Deployment) Settings {
	return Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(deployment.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(deployment.WorkspaceCreationPolicy),
		StatisticsTimeZone:      deployment.StatisticsTimeZone,
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

// Execute 返回当前注册策略、工作区创建策略和统计时区。
func (q *SettingsQuery) Execute(ctx context.Context) (Settings, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	return settingsFromModel(deployment), nil
}

// UpdateSettingsAction 修改部署级策略。
type UpdateSettingsAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewUpdateSettingsAction 创建部署级策略修改操作，enqueuer 投递统计时区变化后的运营数据重建任务。
func NewUpdateSettingsAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *UpdateSettingsAction {
	return &UpdateSettingsAction{db: db, enqueuer: enqueuer}
}

// Execute 校验取值后，由仍有效的部署管理员保存注册策略、工作区创建策略和统计时区；统计时区变化时标记重建并在同一事务内投递汇总任务，立即按新时区从安装日起重建运营数据；任务失败时由定时汇总继续重建。
func (a *UpdateSettingsAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input Settings) (Settings, error) {
	fields := map[string]common.FieldCode{}
	if !input.RegistrationPolicy.Valid() {
		fields["registrationPolicy"] = ValidationRegistrationPolicyInvalid
	}
	if !input.WorkspaceCreationPolicy.Valid() {
		fields["workspaceCreationPolicy"] = ValidationWorkspaceCreationPolicyInvalid
	}
	if !timezone.Valid(input.StatisticsTimeZone) {
		fields["statisticsTimeZone"] = ValidationStatisticsTimeZoneInvalid
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
		timeZoneChanged := deployment.StatisticsTimeZone != input.StatisticsTimeZone
		deployment.StatisticsTimeZone = input.StatisticsTimeZone
		deployment.StatisticsRebuildPending = deployment.StatisticsRebuildPending || timeZoneChanged
		if _, err := tx.NewUpdate().Model(deployment).
			Column("registration_policy", "workspace_creation_policy", "statistics_time_zone", "statistics_rebuild_pending").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		if timeZoneChanged {
			if _, err := a.enqueuer.EnqueueIn(ctx, tx, AggregateStatsActionName, AggregateStatsInput{}, servertask.EnqueueOptions{
				Queue: statsQueue, MaxAttempts: 3,
			}); err != nil {
				return err
			}
		}
		output = settingsFromModel(deployment)
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return output, nil
}
