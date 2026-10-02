//go:build server

package deployment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// WorkspaceListInput 定义部署工作区列表的关键词与分页条件。
type WorkspaceListInput struct {
	Query    string
	Page     int
	PageSize int
}

// WorkspaceRecord 定义部署工作区列表中的一个工作区。
type WorkspaceRecord struct {
	ID          string    `bun:"id"`
	Name        string    `bun:"name"`
	Slug        string    `bun:"slug"`
	MemberCount int       `bun:"member_count"`
	CreatedAt   time.Time `bun:"created_at"`
}

// WorkspaceListOutput 定义部署工作区分页结果。
type WorkspaceListOutput struct {
	Workspaces []WorkspaceRecord
	Page       common.PageInfo
}

// ListWorkspacesQuery 读取部署内的全部工作区。
type ListWorkspacesQuery struct {
	db *bun.DB
}

// NewListWorkspacesQuery 创建部署工作区列表查询。
func NewListWorkspacesQuery(db *bun.DB) *ListWorkspacesQuery {
	return &ListWorkspacesQuery{db: db}
}

// Execute 按关键词返回部署工作区分页列表及各工作区的有效成员数，先创建的工作区在前。
func (q *ListWorkspacesQuery) Execute(ctx context.Context, input WorkspaceListInput) (WorkspaceListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return WorkspaceListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.Where("(o.name ILIKE ? OR o.slug ILIKE ?)", pattern, pattern)
		}
		return query
	}
	total, err := apply(q.db.NewSelect().TableExpr("organizations AS o")).Count(ctx)
	if err != nil {
		return WorkspaceListOutput{}, fmt.Errorf("count deployment workspaces: %w", err)
	}
	workspaces := make([]WorkspaceRecord, 0)
	if err := apply(q.db.NewSelect().TableExpr("organizations AS o")).
		ColumnExpr("o.id::text AS id, o.name, o.slug, o.created_at").
		ColumnExpr("(SELECT count(*) FROM users AS u WHERE u.organization_id = o.id AND u.status = ?) AS member_count", domain.IdentityStatusActive).
		OrderExpr("o.created_at ASC, o.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &workspaces); err != nil {
		return WorkspaceListOutput{}, fmt.Errorf("list deployment workspaces: %w", err)
	}
	return WorkspaceListOutput{Workspaces: workspaces, Page: common.PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
