package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AgentExecutionMode 表示 AI 员工的执行方式。
type AgentExecutionMode = domain.AgentExecutionMode

// CreateAgentInput 定义新增 AI 员工字段，AvatarFileID 为空时不设置头像。
type CreateAgentInput struct {
	DisplayName      string              `json:"displayName"`
	TeamIDs          []string            `json:"teamIds" validate:"dive,uuid" msg:"field.team_invalid"`
	ServiceAudiences []ServiceAudience   `json:"serviceAudiences" validate:"dive,oneof=customer employee" msg:"field.service_audience_invalid"`
	AvatarFileID     string              `json:"avatarFileId"`
	Execution        AgentExecutionInput `json:"execution"`
}

// UpdateAgentInput 定义 AI 员工可编辑字段，AvatarFileID 为空时保留当前头像，HandoffTeamID 为空表示转人工进入公共队列，ResponsibleUserID 为空表示不指定负责人，ComputerID 为空表示不使用工作区电脑，ComputerGrant 是对所用工作区电脑的授权。
type UpdateAgentInput struct {
	DisplayName       string            `json:"displayName"`
	TeamIDs           []string          `json:"teamIds" validate:"dive,uuid" msg:"field.team_invalid"`
	ServiceAudiences  []ServiceAudience `json:"serviceAudiences" validate:"dive,oneof=customer employee" msg:"field.service_audience_invalid"`
	HandoffTeamID     string            `json:"handoffTeamId" validate:"omitempty,uuid" msg:"field.team_invalid"`
	ResponsibleUserID string            `json:"responsibleUserId" validate:"omitempty,uuid" msg:"field.agent_responsible_invalid"`
	ComputerID        string            `json:"computerId" validate:"omitempty,uuid" msg:"field.agent_computer_invalid"`
	ComputerGrant     AgentToolGrant    `json:"computerGrant"`
	LocalAgents       []string          `json:"localAgents"` // 启用的工作区电脑上的本机 Agent 名称，ComputerID 为空时忽略。
	WorkStatus        WorkStatus        `json:"workStatus" validate:"oneof=working away off_duty" msg:"field.work_status_invalid"`
	AvatarFileID      string            `json:"avatarFileId"`
}

// AgentExecutionInput 定义执行配置输入，按执行方式填写 managed。
type AgentExecutionInput struct {
	Mode    AgentExecutionMode          `json:"mode"`
	Managed *AgentManagedExecutionInput `json:"managed,omitempty"`
}

// UpdateAgentExecutionInput 定义运行配置表单整体保存的字段。
type UpdateAgentExecutionInput struct {
	Mode            AgentExecutionMode          `json:"mode"`
	Managed         *AgentManagedExecutionInput `json:"managed,omitempty"`
	BusinessSystems []AgentBusinessSystemGrant  `json:"businessSystems"`
}

// AgentBusinessSystemGrant 定义 AI 员工对一个业务系统的授权：可执行的最高级别、L2 是否需要确认与是否允许对外发信。
type AgentBusinessSystemGrant struct {
	BusinessSystemID string         `json:"id"`
	MaxLevel         OperationLevel `json:"maxLevel"`
	ConfirmL2        bool           `json:"confirmL2"`
	Outbound         bool           `json:"outbound"`
}

// AgentBusinessSystemOption 定义不含凭据的业务系统选择项。
type AgentBusinessSystemOption struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolCount int    `json:"toolCount"`
}

// AgentBusinessSystemOptionList 定义当前工作区的业务系统选择列表。
type AgentBusinessSystemOptionList struct {
	BusinessSystems []AgentBusinessSystemOption `json:"businessSystems"`
}

// AgentManagedExecutionInput 定义平台托管执行配置输入。
type AgentManagedExecutionInput struct {
	ModelID           string   `json:"modelId" validate:"uuid" msg:"field.chat_model_invalid"`
	SystemInstruction string   `json:"systemInstruction" validate:"max=20000" msg:"field.agent_system_instruction_too_long"`
	KnowledgeBaseIDs  []string `json:"knowledgeBaseIds" validate:"dive,uuid" msg:"field.agent_knowledge_base_invalid"`
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

// AgentComputer 定义 AI 员工使用的工作区电脑、授权与启用的本机 Agent 名称。
type AgentComputer struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Grant       AgentToolGrant `json:"grant"`
	LocalAgents []string       `json:"localAgents"`
}

// AgentToolGrant 定义 AI 员工对一组工具的授权：可执行的最高级别与 L2 是否需要确认。
type AgentToolGrant struct {
	MaxLevel  OperationLevel `json:"maxLevel"`
	ConfirmL2 bool           `json:"confirmL2"`
}

// AgentProfile 定义企业 AI 员工对成员公开的资料。
type AgentProfile struct {
	ID          string     `json:"id"`
	IdentityID  string     `json:"identityId"`
	DisplayName string     `json:"displayName"`
	AvatarURL   string     `json:"avatarUrl"`
	Status      UserStatus `json:"status"`
}

// Agent 定义 AI 员工信息，Behavior 是该员工当前生效的内置工作规则与可用工具，HandoffTeamID 为空表示转人工进入公共队列，Responsible 为空表示未指定负责人，Computer 为空表示不使用工作区电脑。
type Agent struct {
	ID               string               `json:"id"`
	IdentityID       string               `json:"identityId"`
	DisplayName      string               `json:"displayName"`
	AvatarURL        string               `json:"avatarUrl"`
	ServiceAudiences []ServiceAudience    `json:"serviceAudiences"`
	HandoffTeamID    *string              `json:"handoffTeamId,omitempty"`
	Responsible      *AgentResponsible    `json:"responsible,omitempty"`
	Computer         *AgentComputer       `json:"computer,omitempty"`
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
	BusinessSystems []AgentBusinessSystemGrant `json:"businessSystems"`
	RevisionID      string                     `json:"revisionId"`
	Mode            AgentExecutionMode         `json:"mode"`
	Managed         *AgentManagedExecution     `json:"managed,omitempty"`
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
