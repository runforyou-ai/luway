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

// AccountListInput 定义平台账号列表的筛选与分页条件。
type AccountListInput struct {
	Query    string
	Status   domain.AccountStatus
	Page     int
	PageSize int
}

// AccountRecord 定义平台账号列表中的一个账号。
type AccountRecord struct {
	ID              string    `bun:"id"`
	Email           string    `bun:"email"`
	DisplayName     string    `bun:"display_name"`
	Status          string    `bun:"status"`
	IsPlatformAdmin bool      `bun:"is_platform_admin"`
	WorkspaceCount  int       `bun:"workspace_count"`
	CreatedAt       time.Time `bun:"created_at"`
}

// AccountListOutput 定义平台账号分页结果。
type AccountListOutput struct {
	Accounts []AccountRecord
	Page     common.PageInfo
}

// accountColumns 选出账号列表字段，工作区数只计有效成员身份。
func accountColumns(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("acc.id::text AS id, acc.email, acc.display_name, acc.status, acc.is_platform_admin, acc.created_at").
		ColumnExpr("(SELECT count(*) FROM users AS u WHERE u.account_id = acc.id AND u.status = ?) AS workspace_count", domain.IdentityStatusActive)
}

// ListAccountsQuery 读取平台内的账号。
type ListAccountsQuery struct {
	db *bun.DB
}

// NewListAccountsQuery 创建平台账号列表查询。
func NewListAccountsQuery(db *bun.DB) *ListAccountsQuery {
	return &ListAccountsQuery{db: db}
}

// Execute 按状态和关键词返回平台账号分页列表，新注册的账号在前。
func (q *ListAccountsQuery) Execute(ctx context.Context, input AccountListInput) (AccountListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	fields := map[string]common.FieldCode{}
	if !pageValid {
		fields["query"] = ValidationQueryInvalid
	}
	if input.Status != domain.AccountStatusActive && input.Status != domain.AccountStatusInactive {
		fields["status"] = ValidationAccountStatusInvalid
	}
	if len(fields) > 0 {
		return AccountListOutput{}, &common.FieldError{Fields: fields}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where("acc.status = ?", input.Status)
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.Where("(acc.email ILIKE ? OR acc.display_name ILIKE ?)", pattern, pattern)
		}
		return query
	}
	total, err := apply(q.db.NewSelect().Model((*servermodels.Account)(nil))).Count(ctx)
	if err != nil {
		return AccountListOutput{}, fmt.Errorf("count platform accounts: %w", err)
	}
	accounts := make([]AccountRecord, 0)
	if err := accountColumns(apply(q.db.NewSelect().Model((*servermodels.Account)(nil)))).
		OrderExpr("acc.created_at DESC, acc.id DESC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &accounts); err != nil {
		return AccountListOutput{}, fmt.Errorf("list platform accounts: %w", err)
	}
	return AccountListOutput{Accounts: accounts, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}

// UpdateAccountAction 修改平台内其他账号的状态或平台管理员身份。
type UpdateAccountAction struct {
	db *bun.DB
}

// NewUpdateAccountAction 创建平台账号修改操作。
func NewUpdateAccountAction(db *bun.DB) *UpdateAccountAction {
	return &UpdateAccountAction{db: db}
}

// SetStatus 停用或恢复其他账号；停用时删除该账号的全部登录会话，并通知各工作区关闭该账号成员的实时连接。
func (a *UpdateAccountAction) SetStatus(ctx context.Context, operator *servermodels.AccountIdentity, accountID string, status domain.AccountStatus) (AccountRecord, error) {
	return a.update(ctx, operator, accountID, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("status = ?", status).
			Set("updated_at = now()").
			Where("id = ?", accountID).
			Exec(ctx); err != nil {
			return err
		}
		if status != domain.AccountStatusInactive {
			return nil
		}
		if _, err := tx.NewDelete().Model((*servermodels.AccountSession)(nil)).Where("account_id = ?", accountID).Exec(ctx); err != nil {
			return err
		}
		var members []servermodels.User
		if err := tx.NewSelect().Model(&members).
			Column("id", "organization_id").
			Where("account_id = ?", accountID).
			Scan(ctx); err != nil {
			return err
		}
		for _, member := range members {
			realtime.Notify(ctx, realtime.UserDisabled(member.OrganizationID, member.ID))
		}
		return nil
	})
}

// SetPlatformAdmin 授予或撤销其他账号的平台管理员身份；授予时目标账号须至少有一个有效的工作区成员身份。
func (a *UpdateAccountAction) SetPlatformAdmin(ctx context.Context, operator *servermodels.AccountIdentity, accountID string, admin bool) (AccountRecord, error) {
	return a.update(ctx, operator, accountID, func(ctx context.Context, tx bun.Tx) error {
		// 授予时检查目标账号在正常状态工作区中的有效成员身份；目标账号行已锁定，与成员停用串行，共享锁定所在工作区，与工作区暂停串行。
		if admin {
			var workspaceIDs []string
			if err := tx.NewSelect().TableExpr("users AS u").
				Join("JOIN organizations AS o ON o.id = u.organization_id").
				ColumnExpr("o.id::text").
				Where("u.account_id = ?", accountID).
				Where("u.status = ?", domain.IdentityStatusActive).
				Where("o.lifecycle_status = ?", domain.OrganizationLifecycleActive).
				OrderExpr("o.id ASC").
				For("SHARE OF o").
				Scan(ctx, &workspaceIDs); err != nil {
				return err
			}
			if len(workspaceIDs) == 0 {
				return ErrNoActiveMembership
			}
		}
		_, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("is_platform_admin = ?", admin).
			Set("updated_at = now()").
			Where("id = ?", accountID).
			Exec(ctx)
		return err
	})
}

// update 锁定全部有效平台管理员和目标账号后执行修改并返回修改后的账号。
//
// 操作者只能修改其他账号，且在锁定后仍须是有效平台管理员，平台因此始终保留至少一名有效平台管理员。
func (a *UpdateAccountAction) update(ctx context.Context, operator *servermodels.AccountIdentity, accountID string, change func(context.Context, bun.Tx) error) (AccountRecord, error) {
	if accountID == operator.Account.ID {
		return AccountRecord{}, ErrSelfChange
	}
	if !common.ValidUUID(accountID) {
		return AccountRecord{}, ErrAccountNotFound
	}
	var output AccountRecord
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		exists, err := tx.NewSelect().Model((*servermodels.Account)(nil)).
			Where("acc.id = ?", accountID).
			For("NO KEY UPDATE").
			Exists(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return ErrAccountNotFound
		}
		if err := change(ctx, tx); err != nil {
			return err
		}
		err = accountColumns(tx.NewSelect().Model((*servermodels.Account)(nil))).
			Where("acc.id = ?", accountID).
			Scan(ctx, &output)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		return err
	})
	if err != nil {
		return AccountRecord{}, err
	}
	return output, nil
}
