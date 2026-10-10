package appservice

import "context"

// TeamPerformanceBackend 定义团队表现报表的业务调用。
type TeamPerformanceBackend interface {
	// GetTeamPerformanceReport 返回当前企业指定范围内的真人客服表现概览。
	//appservice:route GET /reports/team-performance perm=reports.view
	GetTeamPerformanceReport(context.Context, RequestMeta, TeamPerformanceReportInput) (TeamPerformanceReport, error)
	// ListTeamPerformanceMembers 返回按客服拆分的一页真人客服表现。
	//appservice:route GET /reports/team-performance/members perm=reports.view
	ListTeamPerformanceMembers(context.Context, RequestMeta, TeamPerformanceMemberListInput) (TeamPerformanceMemberList, error)
	// ListTeamPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页真人客服表现。
	//appservice:route GET /reports/team-performance/breakdowns perm=reports.view
	ListTeamPerformanceBreakdowns(context.Context, RequestMeta, TeamPerformanceBreakdownInput) (TeamPerformanceBreakdownList, error)
	// ListTeamPerformanceIssues 返回一页指定类型的真人接待问题会话。
	//appservice:route GET /reports/team-performance/issues perm=reports.view
	ListTeamPerformanceIssues(context.Context, RequestMeta, TeamPerformanceIssueListInput) (ServiceIssueList, error)
}
