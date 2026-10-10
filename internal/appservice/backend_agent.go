package appservice

import "context"

// AgentBackend 定义 AI 员工管理、写回复与停止 AI 回复的业务调用。
type AgentBackend interface {
	// ListServiceReplyAgents 返回可用于 AI 写回复的 AI 员工。
	//appservice:route GET /reply-suggestion-agents perm=none
	ListServiceReplyAgents(context.Context, RequestMeta) (ServiceReplyAgentList, error)
	// GenerateServiceReplySuggestions 使用 AI 员工为服务会话生成回复候选。
	//appservice:route POST /conversations/{conversationID:uuid}/reply-suggestions perm=none
	GenerateServiceReplySuggestions(context.Context, RequestMeta, string, ServiceReplySuggestionsInput) (ServiceReplySuggestions, error)
	// StopAgentReply 停止独立 AI 会话中指定的回复并返回实际运行状态。
	//appservice:route POST /agent-conversations/{conversationID:uuid}/runs/{runID:uuid}/stop perm=none
	StopAgentReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// StopGroupAgentReply 停止群聊中指定的 AI 员工回复并返回实际运行状态。
	//appservice:route POST /group-conversations/{conversationID:uuid}/runs/{runID:uuid}/stop perm=none
	StopGroupAgentReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// ListAgentBusinessSystemOptions 返回当前工作区可授权的业务系统。
	//appservice:route GET /agents/business-system-options perm=none
	ListAgentBusinessSystemOptions(context.Context, RequestMeta) (AgentBusinessSystemOptionList, error)
	// CreateAgent 创建企业 AI 员工。
	//appservice:route POST /agents status=201 perm=ai_employees.manage
	CreateAgent(context.Context, RequestMeta, CreateAgentInput) (Agent, error)
	// ListAgents 返回企业 AI 员工目录与当前成员负责的个人 AI 员工，没有 AI 员工管理权限时只返回个人 AI 员工。
	//appservice:route GET /agents perm=none
	ListAgents(context.Context, RequestMeta, AgentListInput) (AgentList, error)
	// GetAgent 返回企业 AI 员工详情。
	//appservice:route GET /agents/{agentID:uuid} perm=ai_employees.manage
	GetAgent(context.Context, RequestMeta, string) (Agent, error)
	// GetAgentProfile 返回企业 AI 员工的公开资料，供成员发起聊天时展示。
	//appservice:route GET /agents/{agentID:uuid}/profile perm=none
	GetAgentProfile(context.Context, RequestMeta, string) (AgentProfile, error)
	// UpdateAgent 修改企业 AI 员工。
	//appservice:route PUT /agents/{agentID:uuid} perm=ai_employees.manage
	UpdateAgent(context.Context, RequestMeta, string, UpdateAgentInput) (Agent, error)
	// UpdateAgentExecution 修改企业 AI 员工的执行配置。
	//appservice:route PUT /agents/{agentID:uuid}/execution perm=ai_employees.manage
	UpdateAgentExecution(context.Context, RequestMeta, string, UpdateAgentExecutionInput) (Agent, error)
	// DeactivateAgent 禁用企业 AI 员工账号。
	//appservice:route POST /agents/{agentID:uuid}/deactivate perm=ai_employees.manage
	DeactivateAgent(context.Context, RequestMeta, string) (Agent, error)
	// ReactivateAgent 恢复企业 AI 员工。
	//appservice:route POST /agents/{agentID:uuid}/reactivate perm=ai_employees.manage
	ReactivateAgent(context.Context, RequestMeta, string) (Agent, error)
}
