//go:build server

package deployment

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

// AccountListInput 定义部署账号列表的筛选与分页条件。
type AccountListInput struct {
	Query    string
	Status   domain.AccountStatus
	Page     int
	PageSize int
}

// AccountRecord 定义部署账号列表中的一个账号。
type AccountRecord struct {
	ID                string    `bun:"id"`
	Email             string    `bun:"email"`
	DisplayName       string    `bun:"display_name"`
	Status            string    `bun:"status"`
	IsDeploymentAdmin bool      `bun:"is_deployment_admin"`
	WorkspaceCount    int       `bun:"workspace_count"`
	CreatedAt         time.Time `bun:"created_at"`
}

// AccountListOutput 定义部署账号分页结果。
type AccountListOutput struct {
	Accounts []AccountRecord
	Page     common.PageInfo
}

// accountColumns 选出账号列表字段，工作区数只计有效成员身份。
func accountColumns(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("acc.id::text AS id, acc.email, acc.display_name, acc.status, acc.is_deployment_admin, acc.created_at").
		ColumnExpr("(SELECT count(*) FROM users AS u WHERE u.account_id = acc.id AND u.status = ?) AS workspace_count", domain.IdentityStatusActive)
}

// ListAccountsQuery 读取部署内的账号。
type ListAccountsQuery struct {
	db *bun.DB
}

// NewListAccountsQuery 创建部署账号列表查询。
func NewListAccountsQuery(db *bun.DB) *ListAccountsQuery {
	return &ListAccountsQuery{db: db}
}

// Execute 按状态和关键词返回部署账号分页列表，新注册的账号在前。
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
		return AccountListOutput{}, fmt.Errorf("count deployment accounts: %w", err)
	}
	accounts := make([]AccountRecord, 0)
	if err := accountColumns(apply(q.db.NewSelect().Model((*servermodels.Account)(nil)))).
		OrderExpr("acc.created_at DESC, acc.id DESC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &accounts); err != nil {
		return AccountListOutput{}, fmt.Errorf("list deployment accounts: %w", err)
	}
	return AccountListOutput{Accounts: accounts, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}

// UpdateAccountAction 修改部署内其他账号的状态或部署管理员身份。
type UpdateAccountAction struct {
	db *bun.DB
}

// NewUpdateAccountAction 创建部署账号修改操作。
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

// SetDeploymentAdmin 授予或撤销其他账号的部署管理员身份。
func (a *UpdateAccountAction) SetDeploymentAdmin(ctx context.Context, operator *servermodels.AccountIdentity, accountID string, admin bool) (AccountRecord, error) {
	return a.update(ctx, operator, accountID, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("is_deployment_admin = ?", admin).
			Set("updated_at = now()").
			Where("id = ?", accountID).
			Exec(ctx)
		return err
	})
}

// update 锁定全部有效部署管理员和目标账号后执行修改并返回修改后的账号。
//
// 操作者只能修改其他账号，且在锁定后仍须是有效部署管理员，部署因此始终保留至少一名有效部署管理员。
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
