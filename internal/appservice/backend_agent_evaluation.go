package appservice

import "context"

// AgentEvaluationBackend 定义 AI 员工评测与评测用例的业务调用。
type AgentEvaluationBackend interface {
	// GetAgentEvaluation 返回 AI 员工评测页的最近两次运行与全部用例。
	//appservice:route GET /agents/{agentID:uuid}/evaluation perm=ai_employees.manage
	GetAgentEvaluation(context.Context, RequestMeta, string) (AgentEvaluation, error)
	// StartAgentEvaluationRun 用 AI 员工当前生效的配置对全部用例发起一次评测运行。
	//appservice:route POST /agents/{agentID:uuid}/evaluation/runs status=201 perm=ai_employees.manage
	StartAgentEvaluationRun(context.Context, RequestMeta, string) error
	// CreateAgentEvaluationCase 为 AI 员工新建手动评测用例。
	//appservice:route POST /agents/{agentID:uuid}/evaluation/cases status=201 perm=ai_employees.manage
	CreateAgentEvaluationCase(context.Context, RequestMeta, string, AgentEvaluationCaseInput) (AgentEvaluationCase, error)
	// GetAgentEvaluationCase 返回评测用例与它在最近一次运行中的全部尝试。
	//appservice:route GET /agents/{agentID:uuid}/evaluation/cases/{caseID:uuid} perm=ai_employees.manage
	GetAgentEvaluationCase(context.Context, RequestMeta, string, string) (AgentEvaluationCaseDetail, error)
	// UpdateAgentEvaluationCase 修改评测用例。
	//appservice:route PUT /agents/{agentID:uuid}/evaluation/cases/{caseID:uuid} perm=ai_employees.manage
	UpdateAgentEvaluationCase(context.Context, RequestMeta, string, string, AgentEvaluationCaseInput) (AgentEvaluationCase, error)
	// DeleteAgentEvaluationCase 删除评测用例。
	//appservice:route DELETE /agents/{agentID:uuid}/evaluation/cases/{caseID:uuid} perm=ai_employees.manage
	DeleteAgentEvaluationCase(context.Context, RequestMeta, string, string) error
	// RerunAgentEvaluationCase 在最近一次运行中重新运行一条用例。
	//appservice:route POST /agents/{agentID:uuid}/evaluation/cases/{caseID:uuid}/rerun perm=ai_employees.manage
	RerunAgentEvaluationCase(context.Context, RequestMeta, string, string) error
	// AddServiceIssueToEvaluation 把应转人工未转的问题会话以选定的客户消息为提问加入负责 AI 员工的评测。
	//appservice:route POST /reports/issues/{serviceSessionID:uuid}/evaluation status=201 perm=ai_employees.manage
	AddServiceIssueToEvaluation(context.Context, RequestMeta, string, ServiceIssueEvaluationInput) (AgentEvaluationCase, error)
}
