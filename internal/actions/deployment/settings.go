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

// Settings 定义部署管理员可修改的部署级策略、运营数据统计时区与运行指标上报开关。
type Settings struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
	StatisticsTimeZone      string
	TelemetryEnabled        bool
}

// settingsFromModel 读取部署实例行中的部署级策略。
func settingsFromModel(deployment *servermodels.Deployment) Settings {
	return Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(deployment.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(deployment.WorkspaceCreationPolicy),
		StatisticsTimeZone:      deployment.StatisticsTimeZone,
		TelemetryEnabled:        deployment.TelemetryEnabled,
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

// Execute 返回当前注册策略、工作区创建策略、统计时区和运行指标上报开关。
func (q *SettingsQuery) Execute(ctx context.Context) (Settings, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	return settingsFromModel(deployment), nil
}

// Policies 定义部署管理员可修改的注册策略和工作区创建策略。
type Policies struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
}

// UpdatePoliciesAction 修改注册策略和工作区创建策略。
type UpdatePoliciesAction struct {
	db *bun.DB
}

// NewUpdatePoliciesAction 创建部署策略修改操作。
func NewUpdatePoliciesAction(db *bun.DB) *UpdatePoliciesAction {
	return &UpdatePoliciesAction{db: db}
}

// Execute 校验策略取值后，由仍有效的部署管理员保存注册策略和工作区创建策略。
func (a *UpdatePoliciesAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input Policies) (Settings, error) {
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
	return updateDeployment(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, deployment *servermodels.Deployment) error {
		deployment.RegistrationPolicy = string(input.RegistrationPolicy)
		deployment.WorkspaceCreationPolicy = string(input.WorkspaceCreationPolicy)
		_, err := tx.NewUpdate().Model(deployment).
			Column("registration_policy", "workspace_creation_policy").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx)
		return err
	})
}

// UpdateStatisticsTimeZoneAction 修改运营数据统计时区。
type UpdateStatisticsTimeZoneAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewUpdateStatisticsTimeZoneAction 创建统计时区修改操作，enqueuer 投递按新时区重建运营数据的汇总任务。
func NewUpdateStatisticsTimeZoneAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *UpdateStatisticsTimeZoneAction {
	return &UpdateStatisticsTimeZoneAction{db: db, enqueuer: enqueuer}
}

// Execute 校验时区后，由仍有效的部署管理员保存统计时区；时区变化时标记重建并在同一事务内投递汇总任务，立即按新时区从安装日起重建运营数据，任务失败时由定时汇总继续重建。
func (a *UpdateStatisticsTimeZoneAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, timeZone string) (Settings, error) {
	if !timezone.Valid(timeZone) {
		return Settings{}, &common.FieldError{Fields: map[string]common.FieldCode{"statisticsTimeZone": ValidationStatisticsTimeZoneInvalid}}
	}
	return updateDeployment(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, deployment *servermodels.Deployment) error {
		if deployment.StatisticsTimeZone == timeZone {
			return nil
		}
		deployment.StatisticsTimeZone = timeZone
		deployment.StatisticsRebuildPending = true
		if _, err := tx.NewUpdate().Model(deployment).
			Column("statistics_time_zone", "statistics_rebuild_pending").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		_, err := a.enqueuer.EnqueueIn(ctx, tx, AggregateStatsActionName, AggregateStatsInput{}, servertask.EnqueueOptions{
			Queue: statsQueue, MaxAttempts: 3,
		})
		return err
	})
}

// updateDeployment 在事务内确认操作者仍是有效部署管理员并锁定部署实例行，执行修改后返回最新的部署设置。
func updateDeployment(ctx context.Context, db *bun.DB, operator *servermodels.AccountIdentity, update func(context.Context, bun.Tx, *servermodels.Deployment) error) (Settings, error) {
	var output Settings
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		deployment, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := update(ctx, tx, deployment); err != nil {
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
