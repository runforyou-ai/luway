package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// PersonalAgentPresence 表示个人 AI 员工当前能否处理新请求。
type PersonalAgentPresence string

const (
	PersonalAgentPresenceOnline   PersonalAgentPresence = PersonalAgentPresence(domain.PersonalAgentPresenceOnline)
	PersonalAgentPresenceOffline  PersonalAgentPresence = PersonalAgentPresence(domain.PersonalAgentPresenceOffline)
	PersonalAgentPresencePaused   PersonalAgentPresence = PersonalAgentPresence(domain.PersonalAgentPresencePaused)
	PersonalAgentPresenceUnbound  PersonalAgentPresence = PersonalAgentPresence(domain.PersonalAgentPresenceUnbound)
	PersonalAgentPresenceInactive PersonalAgentPresence = PersonalAgentPresence(domain.PersonalAgentPresenceInactive)
)

// PersonalAgentInput 定义个人 AI 员工的资料、执行配置与企业 MCP 服务，avatarFileId 为空时保留当前头像。
type PersonalAgentInput struct {
	DisplayName  string              `json:"displayName"`
	AvatarFileID string              `json:"avatarFileId"`
	Execution    AgentExecutionInput `json:"execution"`
	MCPServerIDs []string            `json:"mcpServerIds"`
}

// CreatePersonalAgentInput 定义新建个人 AI 员工的资料、执行配置、企业 MCP 服务与使用的电脑，avatarFileId 为空时不设置头像。
type CreatePersonalAgentInput struct {
	DisplayName  string              `json:"displayName"`
	AvatarFileID string              `json:"avatarFileId"`
	Execution    AgentExecutionInput `json:"execution"`
	MCPServerIDs []string            `json:"mcpServerIds"`
	ComputerID   string              `json:"computerId"`
}

// PersonalAgentComputerInput 定义个人 AI 员工要换到的电脑。
type PersonalAgentComputerInput struct {
	ComputerID string `json:"computerId"`
}

// PersonalAgentResponsible 定义个人 AI 员工负责人的摘要。
type PersonalAgentResponsible struct {
	UserID      string `json:"userId"`
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
}

// PersonalAgentComputer 定义个人 AI 员工使用的电脑的摘要。
type PersonalAgentComputer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PersonalAgent 定义个人 AI 员工信息。
type PersonalAgent struct {
	ID          string                   `json:"id"`
	IdentityID  string                   `json:"identityId"`
	DisplayName string                   `json:"displayName"`
	AvatarURL   string                   `json:"avatarUrl"`
	Responsible PersonalAgentResponsible `json:"responsible"`
	Computer    PersonalAgentComputer    `json:"computer"`
	Status      UserStatus               `json:"status"`
	Presence    PersonalAgentPresence    `json:"presence"`
	Execution   AgentExecutionSummary    `json:"execution"`
	CreatedAt   time.Time                `json:"createdAt"`
}

// PersonalAgentDetail 定义个人 AI 员工信息与当前完整执行配置。
type PersonalAgentDetail struct {
	PersonalAgent PersonalAgent  `json:"personalAgent"`
	Execution     AgentExecution `json:"execution"`
}

// PersonalAgentList 定义个人 AI 员工列表。
type PersonalAgentList struct {
	PersonalAgents []PersonalAgent `json:"personalAgents"`
}

// AgentMemory 定义个人 AI 员工的一条记忆。
type AgentMemory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// AgentMemoryList 定义个人 AI 员工的记忆列表。
type AgentMemoryList struct {
	Memories []AgentMemory `json:"memories"`
}

// AgentMemoryInput 定义负责人编辑记忆时提交的名称、说明与正文。
type AgentMemoryInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}
