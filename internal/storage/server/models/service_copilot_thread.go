//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServiceCopilotThread 表示服务会话中 Copilot 线程的所属服务会话所在会话、AI 员工和创建人。
type ServiceCopilotThread struct {
	bun.BaseModel        `bun:"table:service_copilot_threads,alias:sct"`
	ConversationID       string    `bun:"conversation_id,pk"`
	CreatedAt            time.Time `bun:"created_at"`
	UpdatedAt            time.Time `bun:"updated_at"`
	OrganizationID       string    `bun:"organization_id"`
	ServedConversationID string    `bun:"served_conversation_id"`
	AgentIdentityID      string    `bun:"agent_identity_id"`
	CreatedByIdentityID  string    `bun:"created_by_identity_id"`
}
