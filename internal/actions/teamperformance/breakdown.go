//go:build server

package teamperformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// breakdownMetrics 是各拆分维度共用的汇总列。
var breakdownMetrics = `count(*) AS closed, count(closed.human_requested_at) AS human_requested,
	` + fmt.Sprintf(median, "closed.first_response_seconds") + ` AS first_response_median`

// breakdownSQL 按拆分维度汇总已关闭周期数、需要过真人的周期数与真人首响中位数，渠道按渠道名称分组，咨询分类未分类时 ID 为空。
var breakdownSQL = map[domain.ServiceReportDimension]string{
	domain.ServiceReportDimensionChannel: `
SELECT c.id, c.name, ` + breakdownMetrics + `
FROM closed JOIN channels c ON c.id = closed.channel_id
GROUP BY c.id, c.name`,
	domain.ServiceReportDimensionCategory: `
SELECT sc.id, coalesce(sc.name, '') AS name, ` + breakdownMetrics + `
FROM closed LEFT JOIN service_categories sc ON sc.id = closed.category_id
GROUP BY sc.id, sc.name`,
}

// BreakdownQuery 读取按渠道或咨询分类拆分的团队表现。
type BreakdownQuery struct{ db *bun.DB }

// NewBreakdownQuery 创建团队表现拆分查询。
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
	list := &BreakdownList{Page: page, PageSize: pageSize}
	scope, args := reportScope(identity, input.Input)
	// 总行数与当前页在同一条查询中基于同一份分组结果计算。
	var rows json.RawMessage
	if err := q.db.NewRaw(scope+`, grouped AS (`+grouped+`)
SELECT (SELECT count(*) FROM grouped) AS total,
	(SELECT coalesce(json_agg(p ORDER BY p.position), '[]') FROM (
		SELECT grouped.*, row_number() OVER (ORDER BY closed DESC, name = '' ASC, name ASC, id ASC) AS position
		FROM grouped
		ORDER BY position
		LIMIT ? OFFSET ?
	) p) AS rows`, slices.Concat(args, []any{pageSize, (page - 1) * pageSize})...).Scan(ctx, &list.Total, &rows); err != nil {
		return nil, fmt.Errorf("list team performance breakdown: %w", err)
	}
	if err := json.Unmarshal(rows, &list.Rows); err != nil {
		return nil, fmt.Errorf("decode team performance breakdown: %w", err)
	}
	return list, nil
}
