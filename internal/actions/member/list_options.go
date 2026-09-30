//go:build server

// Package member 实现可分配企业身份与通讯录同事目录查询。
package member

import (
	"context"
	"fmt"
	"strings"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Option 定义可分配的企业身份。
type Option struct {
	ID           string                          `bun:"id"`
	Type         domain.OrganizationIdentityType `bun:"type"`
	DisplayName  string                          `bun:"display_name"`
	AvatarFileID *string                         `bun:"avatar_file_id"`
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
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where("oi.organization_id = ?", identity.Organization.ID).
			Apply(identityaction.ApplyActiveMemberConditions)
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.WhereGroup(" AND ", func(group *bun.SelectQuery) *bun.SelectQuery {
				return group.Where("oi.display_name ILIKE ?", pattern).WhereOr("acc.email ILIKE ?", pattern)
			})
		}
		return query
	}
	base := func() *bun.SelectQuery {
		return q.db.NewSelect().TableExpr("organization_identities AS oi").
			Join("LEFT JOIN users AS u ON u.identity_id = oi.id AND u.organization_id = oi.organization_id").
			Join("LEFT JOIN accounts AS acc ON acc.id = u.account_id").
			Join("LEFT JOIN agents AS a ON a.identity_id = oi.id AND a.organization_id = oi.organization_id")
	}
	total, err := apply(base()).Count(ctx)
	if err != nil {
		return ListOptionsOutput{}, fmt.Errorf("count member options: %w", err)
	}
	members := make([]Option, 0)
	if err := apply(base()).
		ColumnExpr("oi.id::text, oi.type, oi.display_name, oi.avatar_file_id::text").
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &members); err != nil {
		return ListOptionsOutput{}, fmt.Errorf("list member options: %w", err)
	}
	return ListOptionsOutput{Members: members, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
