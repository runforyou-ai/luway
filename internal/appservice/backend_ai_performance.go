package appservice

import "context"

// AIPerformanceBackend 定义 AI 表现报表的业务调用。
type AIPerformanceBackend interface {
	// GetAIPerformanceReport 返回当前企业指定范围内的 AI 客服表现概览。
	//appservice:route GET /reports/ai-performance perm=reports.view
	GetAIPerformanceReport(context.Context, RequestMeta, AIPerformanceReportInput) (AIPerformanceReport, error)
	// ListAIPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页 AI 客服表现。
	//appservice:route GET /reports/ai-performance/breakdowns perm=reports.view
	ListAIPerformanceBreakdowns(context.Context, RequestMeta, AIPerformanceBreakdownInput) (AIPerformanceBreakdownList, error)
	// ListAIPerformanceIssues 返回一页指定类型的 AI 表现问题会话。
	//appservice:route GET /reports/ai-performance/issues perm=reports.view
	ListAIPerformanceIssues(context.Context, RequestMeta, AIPerformanceIssueListInput) (ServiceIssueList, error)
	// ListAgentServiceSessions 返回 AI 员工接待的一页服务周期。
	//appservice:route GET /agents/{agentID:uuid}/service-sessions perm=ai_employees.manage
	ListAgentServiceSessions(context.Context, RequestMeta, string, AgentServiceSessionListInput) (AgentServiceSessionList, error)
}
