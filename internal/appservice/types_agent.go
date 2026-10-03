package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AgentExecutionMode 表示 AI 员工的执行方式。
type AgentExecutionMode string

const (
	AgentExecutionModeManaged AgentExecutionMode = AgentExecutionMode(domain.AgentExecutionModeManaged)
)

// CreateAgentInput 定义新增 AI 员工字段，AvatarFileID 为空时不设置头像。
type CreateAgentInput struct {
	DisplayName      string              `json:"displayName"`
	TeamIDs          []string            `json:"teamIds"`
	ServiceAudiences []ServiceAudience   `json:"serviceAudiences"`
	AvatarFileID     string              `json:"avatarFileId"`
	Execution        AgentExecutionInput `json:"execution"`
}

// UpdateAgentInput 定义 AI 员工可编辑字段，AvatarFileID 为空时保留当前头像，HandoffTeamID 为空表示转人工进入公共队列，ResponsibleUserID 为空表示不指定负责人。
type UpdateAgentInput struct {
	DisplayName       string            `json:"displayName"`
	TeamIDs           []string          `json:"teamIds"`
	ServiceAudiences  []ServiceAudience `json:"serviceAudiences"`
	HandoffTeamID     string            `json:"handoffTeamId"`
	ResponsibleUserID string            `json:"responsibleUserId"`
	WorkStatus        WorkStatus        `json:"workStatus"`
	AvatarFileID      string            `json:"avatarFileId"`
}

// AgentExecutionInput 定义执行配置输入，按执行方式填写 managed。
type AgentExecutionInput struct {
	Mode    AgentExecutionMode          `json:"mode"`
	Managed *AgentManagedExecutionInput `json:"managed,omitempty"`
}

// UpdateAgentExecutionInput 定义运行配置表单整体保存的字段。
type UpdateAgentExecutionInput struct {
	Mode         AgentExecutionMode          `json:"mode"`
	Managed      *AgentManagedExecutionInput `json:"managed,omitempty"`
	MCPServerIDs []string                    `json:"mcpServerIds"`
}

// AgentMCPServerOption 定义不含凭据的 MCP 服务选择项。
type AgentMCPServerOption struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ToolCount      int    `json:"toolCount"`
	CustomerScoped bool   `json:"customerScoped"` // 服务按客户查询，只在客服场景可用。
}

// AgentMCPServerOptionList 定义当前企业的 MCP 服务选择列表。
type AgentMCPServerOptionList struct {
	MCPServers []AgentMCPServerOption `json:"mcpServers"`
}

// AgentManagedExecutionInput 定义平台托管执行配置输入。
type AgentManagedExecutionInput struct {
	ModelID           string   `json:"modelId"`
	SystemInstruction string   `json:"systemInstruction"`
	KnowledgeBaseIDs  []string `json:"knowledgeBaseIds"`
}

// AgentListInput 定义 AI 员工目录查询条件。
type AgentListInput struct {
	Query    string      `json:"query" query:"query"`
	Status   *UserStatus `json:"status,omitempty" query:"status"`
	Page     int         `json:"page" query:"page,default=1"`
	PageSize int         `json:"pageSize" query:"pageSize,default=50"`
}

// AgentBehaviorProfile 定义 AI 员工的内置工作规则与可用工具。
type AgentBehaviorProfile struct {
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools"`
}

// AgentResponsible 定义 AI 员工负责人及其账号状态。
type AgentResponsible struct {
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	Email       string     `json:"email"`
	Status      UserStatus `json:"status"`
}

// Agent 定义 AI 员工信息，Behavior 是该员工当前生效的内置工作规则与可用工具，HandoffTeamID 为空表示转人工进入公共队列，Responsible 为空表示未指定负责人。
type Agent struct {
	ID               string               `json:"id"`
	IdentityID       string               `json:"identityId"`
	DisplayName      string               `json:"displayName"`
	AvatarURL        string               `json:"avatarUrl"`
	ServiceAudiences []ServiceAudience    `json:"serviceAudiences"`
	HandoffTeamID    *string              `json:"handoffTeamId,omitempty"`
	Responsible      *AgentResponsible    `json:"responsible,omitempty"`
	Status           UserStatus           `json:"status"`
	WorkStatus       WorkStatus           `json:"workStatus"`
	Teams            []TeamSummary        `json:"teams"`
	Execution        AgentExecution       `json:"execution"`
	Behavior         AgentBehaviorProfile `json:"behavior"`
	CreatedAt        time.Time            `json:"createdAt"`
}

// AgentListItem 定义 AI 员工目录项，Personal 只在个人 AI 员工上有值。
type AgentListItem struct {
	ID               string                 `json:"id"`
	IdentityID       string                 `json:"identityId"`
	DisplayName      string                 `json:"displayName"`
	AvatarURL        string                 `json:"avatarUrl"`
	ServiceAudiences []ServiceAudience      `json:"serviceAudiences"`
	Status           UserStatus             `json:"status"`
	WorkStatus       WorkStatus             `json:"workStatus"`
	Teams            []TeamSummary          `json:"teams"`
	Execution        AgentExecutionSummary  `json:"execution"`
	Personal         *AgentListPersonalItem `json:"personal,omitempty"`
	CreatedAt        time.Time              `json:"createdAt"`
}

// AgentListPersonalItem 定义个人 AI 员工目录项使用的电脑与在线状态。
type AgentListPersonalItem struct {
	ComputerID   string                `json:"computerId"`
	ComputerName string                `json:"computerName"`
	Presence     PersonalAgentPresence `json:"presence"`
}

// AgentExecution 定义当前生效的执行配置。
type AgentExecution struct {
	MCPServerIDs []string               `json:"mcpServerIds"`
	RevisionID   string                 `json:"revisionId"`
	Mode         AgentExecutionMode     `json:"mode"`
	Managed      *AgentManagedExecution `json:"managed,omitempty"`
}

// AgentManagedExecution 定义平台托管执行配置。
type AgentManagedExecution struct {
	Model             AIModelOption `json:"model"`
	SystemInstruction string        `json:"systemInstruction"`
	KnowledgeBaseIDs  []string      `json:"knowledgeBaseIds"`
}

// AgentExecutionSummary 定义当前执行配置摘要。
type AgentExecutionSummary struct {
	RevisionID string                        `json:"revisionId"`
	Mode       AgentExecutionMode            `json:"mode"`
	Managed    *AgentManagedExecutionSummary `json:"managed,omitempty"`
}

// AgentManagedExecutionSummary 定义平台托管执行配置摘要。
type AgentManagedExecutionSummary struct {
	Model AIModelOption `json:"model"`
}

// AgentList 定义 AI 员工分页结果。
type AgentList struct {
	Agents []AgentListItem `json:"agents"`
	Page   PageInfo        `json:"page"`
}
