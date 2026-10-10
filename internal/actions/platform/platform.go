//go:build server

// Package platform 实现平台记录、平台级策略、平台管理员与运营数据的查询和操作。
package platform

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrNotInstalled 表示平台尚未完成首次安装。
	ErrNotInstalled = errors.New("platform is not installed")
	// ErrNotPlatformAdmin 表示操作者不是有效的平台管理员。
	ErrNotPlatformAdmin = errors.New("account is not an active platform admin")
	// ErrAccountNotFound 表示目标账号不存在。
	ErrAccountNotFound = errors.New("account not found")
	// ErrSelfChange 表示平台管理员试图修改自己的账号状态或管理员身份。
	ErrSelfChange = errors.New("platform admin cannot change own account")
	// ErrNoActiveMembership 表示目标账号在正常状态的工作区中没有有效成员身份，不能设为平台管理员。
	ErrNoActiveMembership = errors.New("account has no active workspace membership")
	// ErrWorkspaceNotFound 表示目标工作区不存在。
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrWorkspaceHasPlatformAdmin 表示工作区中有有效平台管理员成员，不能暂停。
	ErrWorkspaceHasPlatformAdmin = errors.New("workspace has an active platform admin")
)

// 平台管理查询与设置的字段校验码。
const (
	ValidationQueryInvalid common.FieldCode = "PLATFORM_QUERY_INVALID"
)

// DeploymentReloader 刷新本进程缓存的部署状态，本实例修改平台行或授权后调用。
type DeploymentReloader interface {
	Reload(ctx context.Context) error
}

// Create 在首次安装事务内写入平台行，服务器标识与签名私钥由数据库生成，注册仅限受邀邮箱，工作区仅平台管理员可创建，平台时区取平台管理员的时区，部署地址取首次安装时填写的地址。
func Create(ctx context.Context, tx bun.Tx, timeZone, publicURL string) (*servermodels.Platform, error) {
	platform := &servermodels.Platform{
		RegistrationPolicy:      string(domain.RegistrationPolicyInvitationOnly),
		WorkspaceCreationPolicy: string(domain.WorkspaceCreationPolicyPlatformAdmin),
		TimeZone:                timeZone,
		PublicURL:               publicURL,
	}
	if _, err := tx.NewInsert().Model(platform).
		Column("registration_policy", "workspace_creation_policy", "time_zone", "public_url").
		Returning("server_id, created_at, updated_at, server_private_key, telemetry_enabled").
		Exec(ctx); err != nil {
		return nil, err
	}
	return platform, nil
}

// Load 读取平台行；平台尚未完成首次安装时返回 ErrNotInstalled。
func Load(ctx context.Context, db bun.IDB) (*servermodels.Platform, error) {
	return load(ctx, db.NewSelect())
}

// Lock 在调用方事务内读取并锁定平台行，同一时刻只有一个事务修改平台级策略或按策略创建工作区。
func Lock(ctx context.Context, tx bun.Tx) (*servermodels.Platform, error) {
	return load(ctx, tx.NewSelect().For("UPDATE"))
}

// load 按给定查询读取平台行。
func load(ctx context.Context, query *bun.SelectQuery) (*servermodels.Platform, error) {
	platform := &servermodels.Platform{}
	err := query.Model(platform).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, err
	}
	return platform, nil
}

// LockActiveAdmins 按账号编号顺序锁定全部有效平台管理员，并确认操作者仍在其中。
func LockActiveAdmins(ctx context.Context, tx bun.Tx, operator *servermodels.AccountIdentity) error {
	var adminIDs []string
	if err := tx.NewSelect().Model((*servermodels.Account)(nil)).
		ColumnExpr("acc.id::text").
		Where("acc.is_platform_admin").
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
	return ErrNotPlatformAdmin
}

// LockAdmin 以 SHARE 锁定操作者账号并确认其仍是有效平台管理员，锁持有至事务结束。
func LockAdmin(ctx context.Context, tx bun.Tx, operator *servermodels.AccountIdentity) error {
	var admin bool
	err := tx.NewSelect().Model((*servermodels.Account)(nil)).
		ColumnExpr("acc.is_platform_admin AND acc.status = ?", domain.AccountStatusActive).
		Where("acc.id = ?", operator.Account.ID).
		For("SHARE").
		Scan(ctx, &admin)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !admin {
		return ErrNotPlatformAdmin
	}
	return err
}

// ShareWorkspace 以 KEY SHARE 锁定工作区至事务结束，确认工作区存在，不存在时返回 ErrWorkspaceNotFound。
func ShareWorkspace(ctx context.Context, tx bun.Tx, workspaceID string) error {
	if !str.IsUUID(workspaceID) {
		return ErrWorkspaceNotFound
	}
	err := tx.NewSelect().Model((*servermodels.Workspace)(nil)).
		ColumnExpr("o.id::text").
		Where("o.id = ?", workspaceID).
		For("KEY SHARE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWorkspaceNotFound
	}
	return err
}
