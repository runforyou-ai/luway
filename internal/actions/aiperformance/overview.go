//go:build server

package aiperformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/runforyou-ai/cervi/internal/actions/knowledgegap"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// OverviewQuery 读取 AI 表现报表概览。
type OverviewQuery struct{ db *bun.DB }

// NewOverviewQuery 创建 AI 表现报表概览查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery { return &OverviewQuery{db: db} }

// Execute 以一条查询汇总统计范围内已关闭周期的整体计数、满意度、AI 质检与转人工原因分布，并读取所选渠道和 AI 员工范围下全部待处理的待补知识条数。
func (q *OverviewQuery) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Overview, error) {
	scope, args := reportScope(identity, input)
	var result struct {
		Summary
		HandoffReasons json.RawMessage `bun:"handoff_reasons"`
	}
	if err := q.db.NewRaw(scope+`
SELECT count(*) AS closed,
	count(*) FILTER (WHERE resolved) AS resolved,
	count(*) FILTER (WHERE NOT resolved) AS unresolved,
	count(*) FILTER (WHERE ai_only) AS ai_only,
	count(*) FILTER (WHERE ai_only AND resolved) AS ai_resolved,
	count(*) FILTER (WHERE ai_only AND NOT resolved) AS ai_unresolved,
	(SELECT count(DISTINCT service_session_id) FROM handoffs) AS handed_off,
	count(*) FILTER (WHERE close_reason = ?) AS close_ai_resolved,
	count(*) FILTER (WHERE close_reason = ?) AS customer_unresponsive,
	count(*) FILTER (WHERE close_reason = ?) AS manual,
	count(rating_resolved) AS rated,
	count(*) FILTER (WHERE rating_resolved) AS rated_resolved,
	count(*) FILTER (WHERE satisfaction = ?) AS satisfied,
	count(*) FILTER (WHERE satisfaction = ?) AS neutral,
	count(*) FILTER (WHERE satisfaction = ?) AS dissatisfied,
	count(*) FILTER (WHERE ai_incorrect) AS ai_incorrect,
	count(ai_incorrect) AS ai_incorrect_reviewed,
	count(*) FILTER (WHERE ai_missed_handoff) AS ai_missed_handoff,
	count(ai_missed_handoff) AS ai_missed_handoff_reviewed,
	count(*) FILTER (WHERE ai_poor_attitude) AS ai_poor_attitude,
	count(ai_poor_attitude) AS ai_poor_attitude_reviewed,
	(SELECT coalesce(json_agg(r ORDER BY r.count DESC, r.reason ASC), '[]')
		FROM (SELECT reason, count(*) AS count FROM handoffs GROUP BY reason) r) AS handoff_reasons
FROM closed`, slices.Concat(args, []any{
		domain.ServiceSessionCloseAIResolved, domain.ServiceSessionCloseCustomerUnresponsive, domain.ServiceSessionCloseManual,
		domain.ServiceSessionSatisfactionSatisfied, domain.ServiceSessionSatisfactionNeutral, domain.ServiceSessionSatisfactionDissatisfied,
	})...).
		Scan(ctx, &result); err != nil {
		return nil, fmt.Errorf("summarize closed service sessions: %w", err)
	}
	overview := &Overview{Summary: result.Summary}
	if err := json.Unmarshal(result.HandoffReasons, &overview.HandoffReasons); err != nil {
		return nil, fmt.Errorf("decode handoff reasons: %w", err)
	}
	total, err := knowledgegap.PendingCount(ctx, q.db, identity.Organization.ID, knowledgegap.Scope{ChannelID: input.ChannelID, Agents: input.Agents})
	if err != nil {
		return nil, err
	}
	overview.KnowledgeGapTotal = total
	return overview, nil
}
