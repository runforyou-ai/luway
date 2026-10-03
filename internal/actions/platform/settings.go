//go:build server

package platform

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/timezone"
	"github.com/uptrace/bun"
)

// Settings 定义平台管理员可修改的平台级策略、运营数据统计时区与运行指标、错误上报开关。
type Settings struct {
	RegistrationPolicy      domain.RegistrationPolicy
	WorkspaceCreationPolicy domain.WorkspaceCreationPolicy
	StatisticsTimeZone      string
	TelemetryEnabled        bool
}

// settingsFromModel 读取平台行中的平台级策略。
func settingsFromModel(platform *servermodels.Platform) Settings {
	return Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(platform.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(platform.WorkspaceCreationPolicy),
		StatisticsTimeZone:      platform.StatisticsTimeZone,
		TelemetryEnabled:        platform.TelemetryEnabled,
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

// Execute 返回当前注册策略、工作区创建策略、统计时区和运行指标与错误上报开关。
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
	db *bun.DB
}

// NewUpdatePoliciesAction 创建平台策略修改操作。
func NewUpdatePoliciesAction(db *bun.DB) *UpdatePoliciesAction {
	return &UpdatePoliciesAction{db: db}
}

// Execute 校验策略取值后，由仍有效的平台管理员保存注册策略和工作区创建策略。
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
	return updatePlatform(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.RegistrationPolicy = string(input.RegistrationPolicy)
		platform.WorkspaceCreationPolicy = string(input.WorkspaceCreationPolicy)
		_, err := tx.NewUpdate().Model(platform).
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

// Execute 校验时区后，由仍有效的平台管理员保存统计时区；时区变化时标记重建并在同一事务内投递汇总任务，立即按新时区从安装日起重建运营数据，任务失败时由定时汇总继续重建。
func (a *UpdateStatisticsTimeZoneAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, timeZone string) (Settings, error) {
	if !timezone.Valid(timeZone) {
		return Settings{}, &common.FieldError{Fields: map[string]common.FieldCode{"statisticsTimeZone": ValidationStatisticsTimeZoneInvalid}}
	}
	return updatePlatform(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		if platform.StatisticsTimeZone == timeZone {
			return nil
		}
		platform.StatisticsTimeZone = timeZone
		platform.StatisticsRebuildPending = true
		if _, err := tx.NewUpdate().Model(platform).
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

// updatePlatform 在事务内确认操作者仍是有效平台管理员并锁定平台行，执行修改后返回最新的平台设置。
func updatePlatform(ctx context.Context, db *bun.DB, operator *servermodels.AccountIdentity, update func(context.Context, bun.Tx, *servermodels.Platform) error) (Settings, error) {
	var output Settings
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveAdmins(ctx, tx, operator); err != nil {
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
