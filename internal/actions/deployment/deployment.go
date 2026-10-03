//go:build server

// Package deployment 实现部署实例、部署级策略、部署管理员与运营数据的查询和操作。
package deployment

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrNotInstalled 表示部署尚未完成首次安装。
	ErrNotInstalled = errors.New("deployment is not installed")
	// ErrNotDeploymentAdmin 表示操作者不是有效的部署管理员。
	ErrNotDeploymentAdmin = errors.New("account is not an active deployment admin")
	// ErrAccountNotFound 表示目标账号不存在。
	ErrAccountNotFound = errors.New("account not found")
	// ErrSelfChange 表示部署管理员试图修改自己的账号状态或管理员身份。
	ErrSelfChange = errors.New("deployment admin cannot change own account")
	// ErrNoActiveMembership 表示目标账号在正常状态的工作区中没有有效成员身份，不能设为部署管理员。
	ErrNoActiveMembership = errors.New("account has no active workspace membership")
	// ErrWorkspaceNotFound 表示目标工作区不存在。
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrWorkspaceHasDeploymentAdmin 表示工作区中有有效部署管理员成员，不能暂停。
	ErrWorkspaceHasDeploymentAdmin = errors.New("workspace has an active deployment admin")
)

const (
	ValidationQueryInvalid                   common.FieldCode = "DEPLOYMENT_QUERY_INVALID"
	ValidationAccountStatusInvalid           common.FieldCode = "DEPLOYMENT_ACCOUNT_STATUS_INVALID"
	ValidationRegistrationPolicyInvalid      common.FieldCode = "DEPLOYMENT_REGISTRATION_POLICY_INVALID"
	ValidationWorkspaceCreationPolicyInvalid common.FieldCode = "DEPLOYMENT_WORKSPACE_CREATION_POLICY_INVALID"
	ValidationStatisticsTimeZoneInvalid      common.FieldCode = "DEPLOYMENT_STATISTICS_TIME_ZONE_INVALID"
	ValidationWorkspaceSortInvalid           common.FieldCode = "DEPLOYMENT_WORKSPACE_SORT_INVALID"
	ValidationWorkspaceStatusInvalid         common.FieldCode = "DEPLOYMENT_WORKSPACE_STATUS_INVALID"
	ValidationUsageSortInvalid               common.FieldCode = "DEPLOYMENT_USAGE_SORT_INVALID"
	ValidationUsageDaysInvalid               common.FieldCode = "DEPLOYMENT_USAGE_DAYS_INVALID"
)

// Create 在首次安装事务内写入部署实例行，实例标识由数据库生成，注册仅限受邀邮箱，工作区仅部署管理员可创建，统计时区取部署管理员的时区。
func Create(ctx context.Context, tx bun.Tx, statisticsTimeZone string) (*servermodels.Deployment, error) {
	deployment := &servermodels.Deployment{
		RegistrationPolicy:      string(domain.RegistrationPolicyInvitationOnly),
		WorkspaceCreationPolicy: string(domain.WorkspaceCreationPolicyDeploymentAdmin),
		StatisticsTimeZone:      statisticsTimeZone,
	}
	if _, err := tx.NewInsert().Model(deployment).
		Column("registration_policy", "workspace_creation_policy", "statistics_time_zone").
		Returning("instance_id, created_at, updated_at").
		Exec(ctx); err != nil {
		return nil, err
	}
	return deployment, nil
}

// Load 读取部署实例行；部署尚未完成首次安装时返回 ErrNotInstalled。
func Load(ctx context.Context, db bun.IDB) (*servermodels.Deployment, error) {
	return load(ctx, db.NewSelect())
}

// Lock 在调用方事务内读取并锁定部署实例行，同一时刻只有一个事务修改部署级策略或按策略创建工作区。
func Lock(ctx context.Context, tx bun.Tx) (*servermodels.Deployment, error) {
	return load(ctx, tx.NewSelect().For("UPDATE"))
}

// load 按给定查询读取部署实例行。
func load(ctx context.Context, query *bun.SelectQuery) (*servermodels.Deployment, error) {
	deployment := &servermodels.Deployment{}
	err := query.Model(deployment).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, err
	}
	return deployment, nil
}

// Capabilities 返回实例当前生效的能力；实例未激活授权时按免费取值执行。
func Capabilities(_ context.Context, _ bun.IDB) (domain.InstanceCapabilities, error) {
	return domain.FreeInstanceCapabilities(), nil
}

// lockActiveAdmins 按账号编号顺序锁定全部有效部署管理员，并确认操作者仍在其中。
func lockActiveAdmins(ctx context.Context, tx bun.Tx, operator *servermodels.AccountIdentity) error {
	var adminIDs []string
	if err := tx.NewSelect().Model((*servermodels.Account)(nil)).
		ColumnExpr("acc.id::text").
		Where("acc.is_deployment_admin").
		Where("acc.status = ?", domain.AccountStatusActive).
		OrderExpr("acc.id ASC").
		For("NO KEY UPDATE").
		Scan(ctx, &adminIDs); err != nil {
		return err
	}
	for _, id := range adminIDs {
		if id == operator.Account.ID {
			return nil
		}
	}
	return ErrNotDeploymentAdmin
}
