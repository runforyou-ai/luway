package appservice

import "context"

// PersonalAgentBackend 定义个人 AI 员工与 AI 员工记忆的业务调用。
type PersonalAgentBackend interface {
	// ListPersonalAgents 返回当前成员负责的个人 AI 员工。
	//appservice:route GET /personal-agents perm=none
	ListPersonalAgents(context.Context, RequestMeta) (PersonalAgentList, error)
	// ListMemberPersonalAgents 返回指定成员负责的个人 AI 员工。
	//appservice:route GET /users/{userID:uuid}/personal-agents perm=none
	ListMemberPersonalAgents(context.Context, RequestMeta, string) (PersonalAgentList, error)
	// GetPersonalAgent 返回当前成员负责的个人 AI 员工详情。
	//appservice:route GET /personal-agents/{agentID:uuid} perm=none
	GetPersonalAgent(context.Context, RequestMeta, string) (PersonalAgentDetail, error)
	// CreatePersonalAgent 在当前成员的电脑上创建个人 AI 员工。
	//appservice:route POST /personal-agents status=201 perm=none
	CreatePersonalAgent(context.Context, RequestMeta, CreatePersonalAgentInput) (PersonalAgent, error)
	// UpdatePersonalAgent 修改当前成员负责的个人 AI 员工。
	//appservice:route PUT /personal-agents/{agentID:uuid} perm=none
	UpdatePersonalAgent(context.Context, RequestMeta, string, PersonalAgentInput) (PersonalAgent, error)
	// PausePersonalAgent 暂停当前成员负责的个人 AI 员工。
	//appservice:route POST /personal-agents/{agentID:uuid}/pause perm=none
	PausePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ResumePersonalAgent 恢复当前成员负责的已暂停个人 AI 员工。
	//appservice:route POST /personal-agents/{agentID:uuid}/resume perm=none
	ResumePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// MovePersonalAgent 把当前成员负责的个人 AI 员工换到指定电脑。
	//appservice:route PUT /personal-agents/{agentID:uuid}/computer perm=none
	MovePersonalAgent(context.Context, RequestMeta, string, PersonalAgentComputerInput) (PersonalAgent, error)
	// DeactivatePersonalAgent 停用个人 AI 员工。
	//appservice:route POST /personal-agents/{agentID:uuid}/deactivate perm=none
	DeactivatePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ReactivatePersonalAgent 启用已停用的个人 AI 员工。
	//appservice:route POST /personal-agents/{agentID:uuid}/reactivate perm=none
	ReactivatePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ListAgentMemories 返回当前成员负责的个人 AI 员工的记忆，按最近更新排列。
	//appservice:route GET /personal-agents/{agentID:uuid}/memories perm=none
	ListAgentMemories(context.Context, RequestMeta, string) (AgentMemoryList, error)
	// UpdateAgentMemory 修改当前成员负责的个人 AI 员工的一条记忆。
	//appservice:route PUT /personal-agents/{agentID:uuid}/memories/{memoryID:uuid} perm=none
	UpdateAgentMemory(context.Context, RequestMeta, string, string, AgentMemoryInput) (AgentMemory, error)
	// DeleteAgentMemory 删除当前成员负责的个人 AI 员工的一条记忆。
	//appservice:route DELETE /personal-agents/{agentID:uuid}/memories/{memoryID:uuid} perm=none
	DeleteAgentMemory(context.Context, RequestMeta, string, string) error
}
