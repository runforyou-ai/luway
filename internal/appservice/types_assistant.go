package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// AssistantPresence 表示助理当前能否处理新请求。
type AssistantPresence string

const (
	AssistantPresenceOnline   AssistantPresence = AssistantPresence(domain.AssistantPresenceOnline)
	AssistantPresenceOffline  AssistantPresence = AssistantPresence(domain.AssistantPresenceOffline)
	AssistantPresencePaused   AssistantPresence = AssistantPresence(domain.AssistantPresencePaused)
	AssistantPresenceUnbound  AssistantPresence = AssistantPresence(domain.AssistantPresenceUnbound)
	AssistantPresenceInactive AssistantPresence = AssistantPresence(domain.AssistantPresenceInactive)
)

// AssistantInput 定义助理的资料、执行配置与企业 MCP 服务，avatarFileId 为空时保留当前头像。
type AssistantInput struct {
	DisplayName  string              `json:"displayName"`
	AvatarFileID string              `json:"avatarFileId"`
	Execution    AgentExecutionInput `json:"execution"`
	MCPServerIDs []string            `json:"mcpServerIds"`
}

// CreateAssistantInput 定义新建助理的资料、执行配置、企业 MCP 服务与要绑定的本机电脑，avatarFileId 为空时不设置头像。
type CreateAssistantInput struct {
	DisplayName  string              `json:"displayName"`
	AvatarFileID string              `json:"avatarFileId"`
	Execution    AgentExecutionInput `json:"execution"`
	MCPServerIDs []string            `json:"mcpServerIds"`
	DeviceID     string              `json:"deviceId"`
}

// AssistantDeviceInput 定义助理要换到的电脑。
type AssistantDeviceInput struct {
	DeviceID string `json:"deviceId"`
}

// AssistantOwner 定义助理主人的摘要。
type AssistantOwner struct {
	UserID      string `json:"userId"`
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
}

// AssistantDevice 定义助理绑定电脑的摘要，LocalAgents 是该电脑上报的已安装且可用的本机 Agent。
type AssistantDevice struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	LocalAgents []LocalAgentKind `json:"localAgents"`
}

// Assistant 定义助理信息。
type Assistant struct {
	ID          string                `json:"id"`
	IdentityID  string                `json:"identityId"`
	DisplayName string                `json:"displayName"`
	AvatarURL   string                `json:"avatarUrl"`
	Owner       AssistantOwner        `json:"owner"`
	Device      AssistantDevice       `json:"device"`
	Status      UserStatus            `json:"status"`
	Presence    AssistantPresence     `json:"presence"`
	Execution   AgentExecutionSummary `json:"execution"`
	CreatedAt   time.Time             `json:"createdAt"`
}

// AssistantDetail 定义助理信息与当前完整执行配置。
type AssistantDetail struct {
	Assistant Assistant      `json:"assistant"`
	Execution AgentExecution `json:"execution"`
}

// AssistantList 定义助理列表。
type AssistantList struct {
	Assistants []Assistant `json:"assistants"`
}

// AssistantMemory 定义助理的一条记忆。
type AssistantMemory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// AssistantMemoryList 定义助理的记忆列表。
type AssistantMemoryList struct {
	Memories []AssistantMemory `json:"memories"`
}

// AssistantMemoryInput 定义主人编辑记忆时提交的名称、说明与正文。
type AssistantMemoryInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}
