//go:build server

package models

import (
	"github.com/uptrace/bun"
	"time"
)

// AgentConversation 表示 AI 聊天固定的成员和 Agent 归属。
type AgentConversation struct {
	bun.BaseModel      `bun:"table:agent_conversations,alias:ac"`
	ConversationID     string    `bun:"conversation_id,pk"`
	CreatedAt          time.Time `bun:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at"`
	OrganizationID     string    `bun:"organization_id"`
	UserIdentityID     string    `bun:"user_identity_id"`
	AgentIdentityID    string    `bun:"agent_identity_id"`
	MemoryExtractedSeq int64     `bun:"memory_extracted_seq"`
}
