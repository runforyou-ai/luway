//go:build server

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/aiperformance"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// UsageSort 定义业务使用工作区列表的排序方式，均为降序。
type UsageSort string

const (
	UsageSortServiceSessions UsageSort = "service_sessions"
	UsageSortConversations   UsageSort = "conversations"
	UsageSortFirstResponse   UsageSort = "first_response"
	UsageSortKnowledgeGaps   UsageSort = "knowledge_gaps"
	UsageSortModelTokens     UsageSort = "model_tokens"
)

// usageSortOrders 是各排序方式的排序表达式，相同取值按创建时间与编号降序。
var usageSortOrders = map[UsageSort]string{
	UsageSortServiceSessions: "service_sessions DESC, created_at DESC, id DESC",
	UsageSortConversations:   "conversations DESC, created_at DESC, id DESC",
	UsageSortFirstResponse:   "first_response_median DESC NULLS LAST, created_at DESC, id DESC",
	UsageSortKnowledgeGaps:   "knowledge_gaps DESC, created_at DESC, id DESC",
	UsageSortModelTokens:     "input_tokens + output_tokens DESC, created_at DESC, id DESC",
}

// UsageListInput 定义业务使用工作区列表的统计天数、排序与分页；Sort 为空按服务周期数。
type UsageListInput struct {
	Days     int
	Sort     UsageSort
	Page     int
	PageSize int
}

// UsageMetrics 定义最近若干天的业务使用指标。客服指标统计期间关闭的客服周期，口径与工作区的 AI 表现和团队表现报表一致：
// ServiceSessions 为已关闭周期数，Conversations 为其所属会话去重数；AIClosed 为其中 AI 员工接待过的周期数，AIResolved 与 HandedOff 为其中 AI 独立解决与发生过转人工的周期数；
// 首响为按工作时间计的真人首响（秒），没有样本时为空；KnowledgeGaps 为全部待处理的待补知识条数，不受统计天数限制。
// 模型指标统计期间开始的平台模型调用：ModelCalls 为调用数，ModelCallsConcluded 为其中成功、失败与超时的调用数，ModelCallsFailed 为其中失败与超时的调用数；
// InputTokens 含命中缓存的 CachedInputTokens。
type UsageMetrics struct {
	ServiceSessions     int   `json:"service_sessions"`
	Conversations       int   `json:"conversations"`
	AIClosed            int   `json:"ai_closed"`
	AIResolved          int   `json:"ai_resolved"`
	HandedOff           int   `json:"handed_off"`
	FirstResponseMedian *int  `json:"first_response_median"`
	FirstResponseP90    *int  `json:"first_response_p90"`
	KnowledgeGaps       int   `json:"knowledge_gaps"`
	ModelCalls          int   `json:"model_calls"`
	ModelCallsConcluded int   `json:"model_calls_concluded"`
	ModelCallsFailed    int   `json:"model_calls_failed"`
	InputTokens         int64 `json:"input_tokens"`
	CachedInputTokens   int64 `json:"cached_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

// WorkspaceUsage 定义一个工作区的业务使用指标。
type WorkspaceUsage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Status string `json:"lifecycle_status"`
	UsageMetrics
}

// UsageListOutput 定义一页工作区业务使用指标。
type UsageListOutput struct {
	Workspaces []WorkspaceUsage
	Page       common.PageInfo
}

// UsageQuery 读取平台内计入平台规模的工作区的业务使用。
type UsageQuery struct {
	db *bun.DB
}

// NewUsageQuery 创建业务使用查询。
func NewUsageQuery(db *bun.DB) *UsageQuery {
	return &UsageQuery{db: db}
}

// Summary 返回最近 days 天的平台整体业务使用指标，首响在全部周期上计算。
func (q *UsageQuery) Summary(ctx context.Context, days int) (UsageMetrics, error) {
	if days < 1 {
		return UsageMetrics{}, &common.FieldError{Fields: map[string]common.FieldCode{"days": ValidationUsageDaysInvalid}}
	}
	sources, args := usageSources(days)
	var total string
	if err := q.db.NewRaw(sources+`
SELECT json_build_object(
	'service_sessions', team.closed, 'conversations', team.conversations,
	'ai_closed', ai.closed, 'ai_resolved', ai.ai_resolved, 'handed_off', ai.handed_off,
	'first_response_median', team.first_response_median, 'first_response_p90', team.first_response_p90,
	'knowledge_gaps', gaps.pending,
	'model_calls', models.calls, 'model_calls_concluded', models.concluded, 'model_calls_failed', models.failed,
	'input_tokens', models.input_tokens, 'cached_input_tokens', models.cached_input_tokens, 'output_tokens', models.output_tokens)
FROM ai, team, gaps, models
WHERE ai.organization_id IS NULL AND team.organization_id IS NULL AND gaps.organization_id IS NULL AND models.organization_id IS NULL`, args...).Scan(ctx, &total); err != nil {
		return UsageMetrics{}, fmt.Errorf("summarize platform usage: %w", err)
	}
	var metrics UsageMetrics
	if err := json.Unmarshal([]byte(total), &metrics); err != nil {
		return UsageMetrics{}, fmt.Errorf("decode platform usage: %w", err)
	}
	return metrics, nil
}

// ListWorkspaces 按排序返回一页工作区业务使用指标，没有周期或调用的工作区指标为零。
func (q *UsageQuery) ListWorkspaces(ctx context.Context, input UsageListInput) (UsageListOutput, error) {
	if input.Sort == "" {
		input.Sort = UsageSortServiceSessions
	}
	order, sortValid := usageSortOrders[input.Sort]
	fields := map[string]common.FieldCode{}
	if !sortValid {
		fields["sort"] = ValidationUsageSortInvalid
	}
	if input.Days < 1 {
		fields["days"] = ValidationUsageDaysInvalid
	}
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		fields["query"] = ValidationQueryInvalid
	}
	if len(fields) > 0 {
		return UsageListOutput{}, &common.FieldError{Fields: fields}
	}
	sources, args := usageSources(input.Days)
	output := UsageListOutput{Workspaces: []WorkspaceUsage{}, Page: common.PageInfo{Number: input.Page, Size: input.PageSize}}
	// 总行数与当前页在同一条查询中计算。
	var rows json.RawMessage
	if err := q.db.NewRaw(sources+`,
usage AS (
	SELECT o.id::text AS id, o.name, o.slug, o.lifecycle_status, o.created_at,
		coalesce(team.closed, 0) AS service_sessions, coalesce(team.conversations, 0) AS conversations,
		coalesce(ai.closed, 0) AS ai_closed, coalesce(ai.ai_resolved, 0) AS ai_resolved, coalesce(ai.handed_off, 0) AS handed_off,
		team.first_response_median, team.first_response_p90, coalesce(gaps.pending, 0) AS knowledge_gaps,
		coalesce(models.calls, 0) AS model_calls, coalesce(models.concluded, 0) AS model_calls_concluded, coalesce(models.failed, 0) AS model_calls_failed,
		coalesce(models.input_tokens, 0) AS input_tokens, coalesce(models.cached_input_tokens, 0) AS cached_input_tokens, coalesce(models.output_tokens, 0) AS output_tokens
	FROM organizations AS o
	LEFT JOIN ai ON ai.organization_id = o.id
	LEFT JOIN team ON team.organization_id = o.id
	LEFT JOIN gaps ON gaps.organization_id = o.id
	LEFT JOIN models ON models.organization_id = o.id
	WHERE o.lifecycle_status IN (?)
)
SELECT (SELECT count(*) FROM usage) AS total,
	(SELECT coalesce(json_agg(p ORDER BY p.position), '[]') FROM (
		SELECT usage.*, row_number() OVER (ORDER BY `+order+`) AS position
		FROM usage
		ORDER BY position
		LIMIT ? OFFSET ?
	) p) AS rows`, slices.Concat(args, []any{bun.In(listedLifecycleStatuses), input.PageSize, (input.Page - 1) * input.PageSize})...).
		Scan(ctx, &output.Page.Total, &rows); err != nil {
		return UsageListOutput{}, fmt.Errorf("list platform workspace usage: %w", err)
	}
	if err := json.Unmarshal(rows, &output.Workspaces); err != nil {
		return UsageListOutput{}, fmt.Errorf("decode platform workspace usage: %w", err)
	}
	return output, nil
}

// usageSources 返回定义 ai、team、gaps 与 models 四个公共集合的 WITH 子句与参数，各集合按工作区分组并含 organization_id 为空的合计行，只统计计入平台规模的工作区。
func usageSources(days int) (string, []any) {
	listed := []any{bun.In(listedLifecycleStatuses)}
	inListed := func(column string) string {
		return column + " IN (SELECT id FROM organizations WHERE lifecycle_status IN (?))"
	}
	ai, aiArgs := aiperformance.WorkspaceUsageSQL(inListed("ss.organization_id"), listed, days)
	team, teamArgs := teamperformance.WorkspaceUsageSQL(inListed("ss.organization_id"), listed, days)
	gaps, gapArgs := knowledgegap.WorkspacePendingSQL(inListed("kg.organization_id"), listed)
	// 平台模型调用按开始时间计入统计天数，失败率的分母不含进行中与已取消的调用。
	models := `
SELECT amc.organization_id, count(*) AS calls,
	count(*) FILTER (WHERE amc.status IN (?)) AS concluded, count(*) FILTER (WHERE amc.status IN (?)) AS failed,
	coalesce(sum(amc.input_tokens), 0) AS input_tokens, coalesce(sum(amc.cached_input_tokens), 0) AS cached_input_tokens,
	coalesce(sum(amc.output_tokens), 0) AS output_tokens
FROM ai_model_calls AS amc
WHERE amc.model_scope = ? AND amc.created_at >= now() - make_interval(days => ?) AND ` + inListed("amc.organization_id") + `
GROUP BY GROUPING SETS ((amc.organization_id), ())`
	failed := []domain.AIModelCallStatus{domain.AIModelCallStatusFailed, domain.AIModelCallStatusTimedOut}
	modelArgs := slices.Concat([]any{
		bun.In(append([]domain.AIModelCallStatus{domain.AIModelCallStatusSucceeded}, failed...)), bun.In(failed), domain.AIModelScopePlatform, days,
	}, listed)
	return "WITH ai AS (" + ai + "), team AS (" + team + "), gaps AS (" + gaps + "), models AS (" + models + ")",
		slices.Concat(aiArgs, teamArgs, gapArgs, modelArgs)
}
