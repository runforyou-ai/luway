//go:build server

package teamperformance

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// OverviewQuery 读取团队表现报表概览。
type OverviewQuery struct{ db *bun.DB }

// NewOverviewQuery 创建团队表现报表概览查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery { return &OverviewQuery{db: db} }

// Execute 以一条查询汇总统计范围内已关闭周期的真人承接计数、首响与处理时长、真人接待的评价、满意度与质检。
func (q *OverviewQuery) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Summary, error) {
	scope, args := reportScope(identity, input)
	summary := &Summary{}
	if err := q.db.NewRaw(scope+`
SELECT count(*) AS closed,
	count(human_requested_at) AS human_requested,
	count(*) FILTER (WHERE human_requested_at IS NOT NULL AND human_first_response_at IS NOT NULL) AS human_responded,
	count(first_response_seconds) AS first_response_sampled,
	`+fmt.Sprintf(median, "first_response_seconds")+` AS first_response_median,
	`+fmt.Sprintf(p90, "first_response_seconds")+` AS first_response_p90,
	count(ai_handling_seconds) AS ai_handled,
	`+fmt.Sprintf(median, "ai_handling_seconds")+` AS ai_handling_median,
	count(human_handling_seconds) AS human_handled,
	`+fmt.Sprintf(median, "human_handling_seconds")+` AS human_handling_median,
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
