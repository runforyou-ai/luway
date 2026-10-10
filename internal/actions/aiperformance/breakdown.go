//go:build server

package aiperformance

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// breakdownSQL 按拆分维度汇总已关闭周期的已解决数与 AI 独立解决数，渠道按渠道名称分组，咨询分类未分类时 ID 为空。
var breakdownSQL = map[domain.ServiceReportDimension]string{
	domain.ServiceReportDimensionChannel: `
SELECT c.id, c.name, count(*) AS closed, count(*) FILTER (WHERE closed.resolved) AS resolved,
	count(*) FILTER (WHERE closed.ai_only AND closed.resolved) AS ai_resolved
FROM closed JOIN channels c ON c.id = closed.channel_id
GROUP BY c.id, c.name`,
	domain.ServiceReportDimensionCategory: `
SELECT sc.id, coalesce(sc.name, '') AS name, count(*) AS closed, count(*) FILTER (WHERE closed.resolved) AS resolved,
	count(*) FILTER (WHERE closed.ai_only AND closed.resolved) AS ai_resolved
FROM closed LEFT JOIN service_categories sc ON sc.id = closed.category_id
GROUP BY sc.id, sc.name`,
}

// BreakdownQuery 读取按渠道或咨询分类拆分的 AI 表现。
type BreakdownQuery struct{ db *bun.DB }

// NewBreakdownQuery 创建 AI 表现拆分查询。
func NewBreakdownQuery(db *bun.DB) *BreakdownQuery { return &BreakdownQuery{db: db} }

// Execute 返回一页拆分结果，按已关闭周期数从多到少排列，未分类排在同数量的最后。
func (q *BreakdownQuery) Execute(ctx context.Context, identity *servermodels.Identity, input BreakdownInput) (*BreakdownList, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return nil, ErrPageSizeInvalid
	}
	grouped, ok := breakdownSQL[input.Dimension]
	if !ok {
		return nil, ErrDimensionInvalid
	}
	scope, args := reportScope(identity, input.Input)
	// 总行数与当前页在同一条查询中基于同一份分组结果计算。
	total, rows, err := reportpage.Scan[Breakdown](ctx, q.db, scope+`, grouped AS (`+grouped+`)`, "grouped",
		"closed DESC, name = '' ASC, name ASC, id ASC", args, page, pageSize, "ai performance breakdown")
	if err != nil {
		return nil, err
	}
	return &BreakdownList{Rows: rows, Page: page, PageSize: pageSize, Total: total}, nil
}
