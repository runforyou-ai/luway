//go:build server

package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// WorkspaceSort 定义平台工作区列表的排序方式，均为降序。
type WorkspaceSort string

// 平台工作区列表支持的排序方式：创建时间、最近活跃、成员数与存储用量。
const (
	WorkspaceSortCreatedAt   WorkspaceSort = "created_at"
	WorkspaceSortLastActive  WorkspaceSort = "last_active"
	WorkspaceSortMemberCount WorkspaceSort = "member_count"
	WorkspaceSortStorage     WorkspaceSort = "storage"
)

// workspaceSortOrders 是各排序方式的排序表达式，相同取值按创建时间与编号降序。
var workspaceSortOrders = map[WorkspaceSort]string{
	WorkspaceSortCreatedAt:   "o.created_at DESC, o.id DESC",
	WorkspaceSortLastActive:  "last_active_days ASC NULLS LAST, o.created_at DESC, o.id DESC",
	WorkspaceSortMemberCount: "member_count DESC, o.created_at DESC, o.id DESC",
	WorkspaceSortStorage:     "storage_bytes DESC, o.created_at DESC, o.id DESC",
}

// WorkspaceListInput 定义平台工作区列表的关键词、状态、排序与分页条件；Status 为空表示全部状态，Sort 为空按创建时间。
type WorkspaceListInput struct {
	Query    string
	Status   domain.WorkspaceLifecycleStatus
	Sort     WorkspaceSort
	Page     int
	PageSize int
}

// WorkspaceRecord 定义平台工作区列表中的一个工作区及其当前规模；HasAdmin 表示有有效平台管理员成员，LastActiveDays 是按平台时区最近有活跃的日期距今天的天数，从未活跃时为空。
type WorkspaceRecord struct {
	ID              string    `bun:"id"`
	Name            string    `bun:"name"`
	Slug            string    `bun:"slug"`
	Status          string    `bun:"lifecycle_status"`
	MemberCount     int       `bun:"member_count"`
	AIEmployeeCount int       `bun:"ai_employee_count"`
	ChannelCount    int       `bun:"channel_count"`
	ComputerCount   int       `bun:"computer_count"`
	HasAdmin        bool      `bun:"has_admin"`
	StorageBytes    int64     `bun:"storage_bytes"`
	LastActiveDays  *int      `bun:"last_active_days"`
	CreatedAt       time.Time `bun:"created_at"`
}

// WorkspaceListOutput 定义平台工作区分页结果。
type WorkspaceListOutput struct {
	Workspaces []WorkspaceRecord
	Page       common.PageInfo
}

// ListWorkspacesQuery 读取平台内的全部工作区。
type ListWorkspacesQuery struct {
	db *bun.DB
}

// NewListWorkspacesQuery 创建平台工作区列表查询。
func NewListWorkspacesQuery(db *bun.DB) *ListWorkspacesQuery {
	return &ListWorkspacesQuery{db: db}
}

// Execute 按关键词与状态返回平台工作区分页列表及各工作区的当前规模和最近活跃距今天数。
func (q *ListWorkspacesQuery) Execute(ctx context.Context, input WorkspaceListInput) (WorkspaceListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	if input.Sort == "" {
		input.Sort = WorkspaceSortCreatedAt
	}
	order := workspaceSortOrders[input.Sort]
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return WorkspaceListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where("o.lifecycle_status IN (?)", bun.List(listedLifecycleStatuses))
		if input.Status != "" {
			query = query.Where("o.lifecycle_status = ?", input.Status)
		}
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.Where("(o.name ILIKE ? OR o.slug ILIKE ?)", pattern, pattern)
		}
		return query
	}
	total, err := apply(q.db.NewSelect().TableExpr("workspaces AS o")).Count(ctx)
	if err != nil {
		return WorkspaceListOutput{}, fmt.Errorf("count platform workspaces: %w", err)
	}
	workspaces := make([]WorkspaceRecord, 0)
	if err := apply(selectWorkspaceRecords(q.db)).
		OrderExpr(order).
		Limit(int64(input.PageSize)).
		Offset(int64((input.Page-1)*input.PageSize)).
		Scan(ctx, &workspaces); err != nil {
		return WorkspaceListOutput{}, fmt.Errorf("list platform workspaces: %w", err)
	}
	return WorkspaceListOutput{Workspaces: workspaces, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: int(total)}}, nil
}

// selectWorkspaceRecords 返回读取工作区列表行的查询，规模列取当前值，最近活跃距今天数取按日指标，按数据库时钟与平台时区计算，统计日期晚于今天时取 0。
func selectWorkspaceRecords(db bun.IDB) *bun.SelectQuery {
	return workspaceScaleColumns(db.NewSelect().TableExpr("workspaces AS o").
		ColumnExpr("o.id::text AS id, o.name, o.slug, o.lifecycle_status, o.created_at").
		ColumnExpr("EXISTS (?) AS has_admin", workspaceAdminQuery(db)).
		ColumnExpr(`(
			SELECT GREATEST((now() AT TIME ZONE pf.time_zone)::date - last_active.stat_date, 0)
			FROM platforms AS pf, (
				SELECT max(wds.stat_date) AS stat_date FROM workspace_daily_stats AS wds
				WHERE wds.workspace_id = o.id AND (wds.active_account_count > 0 OR wds.message_count > 0)
			) AS last_active
			WHERE last_active.stat_date IS NOT NULL
			LIMIT 1
		) AS last_active_days`))
}

// workspaceAdminQuery 返回别名为 o 的工作区中有效平台管理员成员的子查询。
func workspaceAdminQuery(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("users AS admin_u").
		Join("JOIN accounts AS admin_acc ON admin_acc.id = admin_u.account_id").
		ColumnExpr("1").
		Where("admin_u.workspace_id = o.id").
		Where("admin_u.status = ?", domain.IdentityStatusActive).
		Where("admin_acc.is_platform_admin").
		Where("admin_acc.status = ?", domain.AccountStatusActive)
}

// workspaceScaleColumns 为别名为 o 的工作区查询加入当前规模列：有效成员、有效 AI 员工、已启用渠道、未撤销电脑与已保存文件字节数。
func workspaceScaleColumns(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("(SELECT count(*) FROM users AS u WHERE u.workspace_id = o.id AND u.status = ?) AS member_count", domain.IdentityStatusActive).
		ColumnExpr("(SELECT count(*) FROM agents AS a WHERE a.workspace_id = o.id AND a.status = ?) AS ai_employee_count", domain.IdentityStatusActive).
		ColumnExpr("(SELECT count(*) FROM channels AS c WHERE c.workspace_id = o.id AND c.enabled) AS channel_count").
		ColumnExpr("(SELECT count(*) FROM computers AS cmp WHERE cmp.workspace_id = o.id AND cmp.revoked_at IS NULL) AS computer_count").
		ColumnExpr("(SELECT COALESCE(sum(f.byte_size), 0) FROM files AS f WHERE f.workspace_id = o.id AND f.status = ?) AS storage_bytes", domain.FileStatusActive)
}

// SetWorkspaceStatusAction 暂停或恢复工作区。
type SetWorkspaceStatusAction struct {
	db *bun.DB
}

// NewSetWorkspaceStatusAction 创建工作区暂停与恢复操作。
func NewSetWorkspaceStatusAction(db *bun.DB) *SetWorkspaceStatusAction {
	return &SetWorkspaceStatusAction{db: db}
}

// Execute 由仍有效的平台管理员把工作区切换为正常或暂停状态；有有效平台管理员成员的工作区不能暂停，恢复时在同一事务内重新投递暂停期间挂起的后台任务，状态变化后通知成员、网站访客与电脑重新认证，工作区已处于目标状态时直接返回。
func (a *SetWorkspaceStatusAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, workspaceID string, status domain.WorkspaceLifecycleStatus) (WorkspaceRecord, error) {
	var record WorkspaceRecord
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := LockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		workspace := &servermodels.Workspace{}
		err := tx.NewSelect().Model(workspace).
			Where("o.id = ?", workspaceID).
			Where("o.lifecycle_status IN (?)", bun.List(listedLifecycleStatuses)).
			For("NO KEY UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkspaceNotFound
		}
		if err != nil {
			return err
		}
		if status == domain.WorkspaceLifecycleSuspended && workspace.LifecycleStatus != string(status) {
			// 工作区行已锁定，与授予平台管理员时对所在工作区的共享锁串行。
			hasAdmin, err := tx.NewSelect().TableExpr("workspaces AS o").Where("o.id = ?", workspaceID).
				Where("EXISTS (?)", workspaceAdminQuery(tx)).Exists(ctx)
			if err != nil {
				return err
			}
			if hasAdmin {
				return ErrWorkspaceHasPlatformAdmin
			}
		}
		if workspace.LifecycleStatus != string(status) {
			if _, err := tx.NewUpdate().Model(workspace).
				Set("lifecycle_status = ?", status).
				WherePK().
				Exec(ctx); err != nil {
				return err
			}
			// 有效成员的连接全部关闭并按新状态重连；暂停时同时强制关闭网站访客与电脑连接。
			var userIDs []string
			if err := tx.NewSelect().Model((*servermodels.User)(nil)).ColumnExpr("id::text").
				Where("workspace_id = ? AND status = ?", workspaceID, domain.IdentityStatusActive).Scan(ctx, &userIDs); err != nil {
				return err
			}
			for _, userID := range userIDs {
				realtime.Notify(ctx, realtime.UserWorkspaceStatusChanged(workspaceID, userID))
			}
			if status == domain.WorkspaceLifecycleSuspended {
				var computerIDs []string
				if err := tx.NewSelect().Model((*servermodels.Computer)(nil)).ColumnExpr("id::text").Where("workspace_id = ? AND revoked_at IS NULL", workspaceID).Scan(ctx, &computerIDs); err != nil {
					return err
				}
				for _, computerID := range computerIDs {
					realtime.Notify(ctx, realtime.ComputerCredentialRevoked(workspaceID, computerID))
				}
				var channelIDs []string
				if err := tx.NewSelect().Model((*servermodels.Channel)(nil)).ColumnExpr("id::text").
					Where("workspace_id = ? AND type = ?", workspaceID, domain.ChannelTypeWebsite).Scan(ctx, &channelIDs); err != nil {
					return err
				}
				for _, channelID := range channelIDs {
					realtime.Notify(ctx, realtime.WebsiteChannelDisabled(workspaceID, channelID))
				}
			}
		}
		return selectWorkspaceRecords(tx).Where("o.id = ?", workspaceID).Scan(ctx, &record)
	})
	if err != nil {
		return WorkspaceRecord{}, err
	}
	return record, nil
}
