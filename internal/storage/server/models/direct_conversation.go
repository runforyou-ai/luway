//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// DirectConversation 表示内部单聊的规范化身份对。
type DirectConversation struct {
	bun.BaseModel `bun:"table:direct_conversations,alias:dc"`

	ConversationID   string    `bun:"conversation_id,pk"`
	CreatedAt        time.Time `bun:"created_at"`
	UpdatedAt        time.Time `bun:"updated_at"`
	OrganizationID   string    `bun:"organization_id"`
	FirstIdentityID  string    `bun:"first_identity_id"`
	SecondIdentityID string    `bun:"second_identity_id"`
}
