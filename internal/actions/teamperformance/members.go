//go:build server

package teamperformance

import (
	"context"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// MemberListQuery 读取按客服拆分的团队表现。
type MemberListQuery struct{ db *bun.DB }

// NewMemberListQuery 创建客服表现查询。
func NewMemberListQuery(db *bun.DB) *MemberListQuery { return &MemberListQuery{db: db} }

// Execute 返回一页客服表现，按关闭时由其负责的周期数从多到少排列，同数量按名称排列。
func (q *MemberListQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ListInput) (*MemberList, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return nil, ErrPageSizeInvalid
	}
	scope, args := reportScope(identity, input.Input)
	// 总行数与当前页在同一条查询中基于同一份分组结果计算。
	total, rows, err := reportpage.Scan[Member](ctx, q.db, scope+`, grouped AS (
	SELECT oi.id, oi.display_name, oi.avatar_file_id, count(*) AS closed,
		count(*) FILTER (WHERE closed.satisfaction = ?) AS satisfied,
		count(closed.satisfaction) AS satisfaction_judged,
		count(*) FILTER (WHERE closed.satisfaction = ? OR closed.human_incorrect OR closed.human_poor_attitude) AS issues
	FROM closed JOIN workspace_identities oi ON oi.workspace_id = closed.workspace_id AND oi.id = closed.member_id
	GROUP BY oi.id, oi.display_name, oi.avatar_file_id
)`, "grouped", "closed DESC, display_name ASC, id ASC",
		slices.Concat(args, []any{domain.ServiceSessionSatisfactionSatisfied, domain.ServiceSessionSatisfactionDissatisfied}),
		page, pageSize, "team performance members")
	if err != nil {
		return nil, err
	}
	return &MemberList{Rows: rows, Page: page, PageSize: pageSize, Total: total}, nil
}
