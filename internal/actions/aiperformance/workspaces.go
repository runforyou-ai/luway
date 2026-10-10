//go:build server

package aiperformance

// 概览与按工作区汇总共用的计数表达式：AI 独立解决的周期数与发生过转人工的周期数。
const (
	aiResolvedCount = "count(*) FILTER (WHERE ai_only AND resolved)"
	handedOffCount  = "count(*) FILTER (WHERE EXISTS (SELECT 1 FROM handoffs WHERE handoffs.service_session_id = closed.id))"
)

// WorkspaceUsageSQL 返回按工作区汇总最近 days 天内 AI 员工接待过的已关闭周期的查询与参数，结果另含 workspace_id 为空的合计行；
// workspaces 为限定 ss.workspace_id 的工作区条件，结果列为 workspace_id、closed、ai_resolved 与 handed_off。
func WorkspaceUsageSQL(workspaces string, workspaceArgs []any, days int) (string, []any) {
	scope, args := scopeSQL(workspaces, workspaceArgs, days, "", nil)
	return scope + `
SELECT closed.workspace_id, count(*) AS closed, ` + aiResolvedCount + ` AS ai_resolved, ` + handedOffCount + ` AS handed_off
FROM closed
GROUP BY GROUPING SETS ((closed.workspace_id), ())`, args
}
