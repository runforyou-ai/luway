//go:build server

package aiperformance

// 概览与按工作区汇总共用的计数表达式：AI 独立解决的周期数与发生过转人工的周期数。
const (
	aiResolvedCount = "count(*) FILTER (WHERE ai_only AND resolved)"
	handedOffCount  = "count(*) FILTER (WHERE EXISTS (SELECT 1 FROM handoffs WHERE handoffs.service_session_id = closed.id))"
)

// WorkspaceUsageSQL 返回按工作区汇总最近 days 天内 AI 员工接待过的已关闭周期的查询与参数，结果另含 organization_id 为空的合计行；
// organizations 为限定 ss.organization_id 的工作区条件，结果列为 organization_id、closed、ai_resolved 与 handed_off。
func WorkspaceUsageSQL(organizations string, organizationArgs []any, days int) (string, []any) {
	scope, args := scopeSQL(organizations, organizationArgs, days, "", nil)
	return scope + `
SELECT closed.organization_id, count(*) AS closed, ` + aiResolvedCount + ` AS ai_resolved, ` + handedOffCount + ` AS handed_off
FROM closed
GROUP BY GROUPING SETS ((closed.organization_id), ())`, args
}
