//go:build server

// Package member 实现可分配企业身份与通讯录同事目录查询。
package member

import (
	"context"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Option 定义可分配的企业身份。
type Option struct {
	ID           string                       `bun:"id"`
	Type         domain.WorkspaceIdentityType `bun:"type"`
	DisplayName  string                       `bun:"display_name"`
	AvatarFileID *string                      `bun:"avatar_file_id"`
}

// ListOptionsInput 定义成员选择项查询条件。
type ListOptionsInput struct {
	Query    string
	Page     int
	PageSize int
}

// ListOptionsOutput 定义成员选择项分页结果。
type ListOptionsOutput struct {
	Members []Option
	Page    common.PageInfo
}

// ListOptionsQuery 读取可分配的企业身份。
type ListOptionsQuery struct{ db *bun.DB }

// NewListOptionsQuery 创建企业身份选择项查询。
func NewListOptionsQuery(db *bun.DB) *ListOptionsQuery { return &ListOptionsQuery{db: db} }

// Execute 返回当前企业中可分配的用户和 AI 员工身份。
func (q *ListOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListOptionsInput) (ListOptionsOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return ListOptionsOutput{}, ErrQueryInvalid
	}
	members := make([]Option, 0)
	total, err := identityDirectory(q.db, identity.Workspace.ID, input.Query).
		Apply(identityaction.ApplyActiveMemberConditions).
		ColumnExpr("oi.id::text, oi.type, oi.display_name, oi.avatar_file_id::text").
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Limit(int64(input.PageSize)).
		Offset(int64((input.Page-1)*input.PageSize)).
		ScanAndCount(ctx, &members)
	if err != nil {
		return ListOptionsOutput{}, fmt.Errorf("list member options: %w", err)
	}
	return ListOptionsOutput{Members: members, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: int(total)}}, nil
}

// identityDirectory 返回本企业关联用户、账号与 AI 员工的企业身份查询，keyword 非空时按名称或邮箱包含匹配。
func identityDirectory(db *bun.DB, workspaceID, keyword string) *bun.SelectQuery {
	query := db.NewSelect().TableExpr("workspace_identities AS oi").
		Join("LEFT JOIN users AS u ON u.identity_id = oi.id AND u.workspace_id = oi.workspace_id").
		Join("LEFT JOIN accounts AS acc ON acc.id = u.account_id").
		Join("LEFT JOIN agents AS a ON a.identity_id = oi.id AND a.workspace_id = oi.workspace_id").
		Where("oi.workspace_id = ?", workspaceID)
	if keyword != "" {
		pattern := common.ContainsPattern(keyword)
		query = query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
			return group.Where("oi.display_name ILIKE ?", pattern).WhereOr("acc.email ILIKE ?", pattern)
		})
	}
	return query
}
