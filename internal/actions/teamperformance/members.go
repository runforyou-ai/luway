//go:build server

package teamperformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	list := &MemberList{Page: page, PageSize: pageSize}
	scope, args := reportScope(identity, input.Input)
	// 总行数与当前页在同一条查询中基于同一份分组结果计算。
	var rows json.RawMessage
	if err := q.db.NewRaw(scope+`, grouped AS (
	SELECT oi.id, oi.display_name, oi.avatar_file_id, count(*) AS closed,
		count(*) FILTER (WHERE closed.satisfaction = ?) AS satisfied,
		count(closed.satisfaction) AS satisfaction_judged,
		count(*) FILTER (WHERE closed.satisfaction = ? OR closed.human_incorrect OR closed.human_poor_attitude) AS issues
	FROM closed JOIN organization_identities oi ON oi.organization_id = closed.organization_id AND oi.id = closed.member_id
	GROUP BY oi.id, oi.display_name, oi.avatar_file_id
)
SELECT (SELECT count(*) FROM grouped) AS total,
	(SELECT coalesce(json_agg(p ORDER BY p.position), '[]') FROM (
		SELECT grouped.*, row_number() OVER (ORDER BY closed DESC, display_name ASC, id ASC) AS position
		FROM grouped
		ORDER BY position
		LIMIT ? OFFSET ?
	) p) AS rows`, slices.Concat(args, []any{domain.ServiceSessionSatisfactionSatisfied, domain.ServiceSessionSatisfactionDissatisfied, pageSize, (page - 1) * pageSize})...).Scan(ctx, &list.Total, &rows); err != nil {
		return nil, fmt.Errorf("list team performance members: %w", err)
	}
	if err := json.Unmarshal(rows, &list.Rows); err != nil {
		return nil, fmt.Errorf("decode team performance members: %w", err)
	}
	return list, nil
}
