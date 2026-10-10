package appservice

import "context"

// ServiceIssueBackend 定义服务问题详情的业务调用。
type ServiceIssueBackend interface {
	// GetServiceIssue 返回客服周期的质检结论与对客沟通。
	//appservice:route GET /reports/issues/{serviceSessionID:uuid} perm=reports.view
	GetServiceIssue(context.Context, RequestMeta, string) (ServiceIssueDetail, error)
}
