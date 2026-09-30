//go:build server

// Package team 实现企业团队及成员关系的查询和操作。
package team

import (
	"context"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListTeamsQuery 读取当前企业的团队列表。
type ListTeamsQuery struct{ db *bun.DB }

// NewListTeamsQuery 创建团队列表查询。
func NewListTeamsQuery(db *bun.DB) *ListTeamsQuery { return &ListTeamsQuery{db: db} }

// Execute 返回满足条件的团队分页列表。
func (q *ListTeamsQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListInput) (ListOutput, error) {
	input.Query = strings.TrimSpace(input.Query)
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return ListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	apply := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where("t.organization_id = ?", identity.Organization.ID)
		if input.Query != "" {
			pattern := common.ContainsPattern(input.Query)
			query = query.Where("(t.name ILIKE ? OR t.description ILIKE ?)", pattern, pattern)
		}
		return query
	}
	total, err := apply(q.db.NewSelect().TableExpr("teams AS t")).Count(ctx)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count teams: %w", err)
	}
	teams := make([]TeamRecord, 0)
	query := apply(q.db.NewSelect().TableExpr("teams AS t")).
		ColumnExpr("t.id::text AS id").
		Column("name", "description", "created_at", "updated_at")
	err = withActiveMemberCount(query).
		OrderExpr("lower(t.name) ASC, t.id ASC").
		Limit(input.PageSize).
		Offset((input.Page-1)*input.PageSize).
		Scan(ctx, &teams)
	if err != nil {
		return ListOutput{}, fmt.Errorf("list teams: %w", err)
	}
	return ListOutput{Teams: teams, Page: PageInfo{Number: input.Page, Size: input.PageSize, Total: total}}, nil
}
