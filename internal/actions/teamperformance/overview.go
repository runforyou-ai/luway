//go:build server

package teamperformance

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// OverviewQuery 读取团队表现报表概览。
type OverviewQuery struct{ db *bun.DB }

// NewOverviewQuery 创建团队表现报表概览查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery { return &OverviewQuery{db: db} }

// Execute 以一条查询汇总统计范围内已关闭周期的真人承接计数、首响与处理时长、真人接待的评价、满意度与质检。
func (q *OverviewQuery) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Summary, error) {
	if err := reportpage.ValidateServiceScope(ctx, q.db, identity.Workspace.ID, input.Audience, input.ServiceSource, input.ChannelID); err != nil {
		return nil, err
	}
	scope, args := reportScope(identity, input)
	summary := &Summary{}
	if err := q.db.NewRaw(scope+`
SELECT count(*) AS closed,
	count(human_requested_at) AS human_requested,
	count(*) FILTER (WHERE human_requested_at IS NOT NULL AND human_first_response_at IS NOT NULL) AS human_responded,
	count(first_response_seconds) AS first_response_sampled,
	`+reportpage.MedianExpr("first_response_seconds")+` AS first_response_median,
	`+reportpage.P90Expr("first_response_seconds")+` AS first_response_p90,
	count(ai_handling_seconds) AS ai_handled,
	`+reportpage.MedianExpr("ai_handling_seconds")+` AS ai_handling_median,
	count(human_handling_seconds) AS human_handled,
	`+reportpage.MedianExpr("human_handling_seconds")+` AS human_handling_median,
	count(rating_resolved) FILTER (WHERE human_assigned_at IS NOT NULL) AS rated,
	count(*) FILTER (WHERE human_assigned_at IS NOT NULL AND rating_resolved) AS rated_resolved,
	count(*) FILTER (WHERE human_assigned_at IS NOT NULL AND satisfaction = ?) AS satisfied,
	count(*) FILTER (WHERE human_assigned_at IS NOT NULL AND satisfaction = ?) AS neutral,
	count(*) FILTER (WHERE human_assigned_at IS NOT NULL AND satisfaction = ?) AS dissatisfied,
	count(*) FILTER (WHERE human_incorrect) AS human_incorrect,
	count(human_incorrect) AS human_incorrect_reviewed,
	count(*) FILTER (WHERE human_poor_attitude) AS human_poor_attitude,
	count(human_poor_attitude) AS human_poor_attitude_reviewed
FROM closed`, slices.Concat(args, []any{
		domain.ServiceSessionSatisfactionSatisfied, domain.ServiceSessionSatisfactionNeutral, domain.ServiceSessionSatisfactionDissatisfied,
	})...).Scan(ctx, summary); err != nil {
		return nil, fmt.Errorf("summarize team performance: %w", err)
	}
	return summary, nil
}
