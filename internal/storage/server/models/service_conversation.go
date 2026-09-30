//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServiceConversation 表示会话中由组织负责处理发起人请求的服务会话。
type ServiceConversation struct {
	bun.BaseModel `bun:"table:service_conversations,alias:svc"`

	ID                      string    `bun:"id,pk"`
	CreatedAt               time.Time `bun:"created_at"`
	UpdatedAt               time.Time `bun:"updated_at"`
	OrganizationID          string    `bun:"organization_id"`
	ConversationID          string    `bun:"conversation_id"`
	Source                  string    `bun:"source"`
	RequesterSubjectID      string    `bun:"requester_subject_id"`
	Audience                string    `bun:"audience"`
	CurrentServiceSessionID *string   `bun:"current_service_session_id"`
}
