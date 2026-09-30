//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelConversation 表示渠道会话与联系人渠道身份的关系。
type ChannelConversation struct {
	bun.BaseModel `bun:"table:channel_conversations,alias:cc"`

	ConversationID           string     `bun:"conversation_id,pk"`
	CreatedAt                time.Time  `bun:"created_at"`
	UpdatedAt                time.Time  `bun:"updated_at"`
	OrganizationID           string     `bun:"organization_id"`
	ContactChannelIdentityID string     `bun:"contact_channel_identity_id"`
	ReplyLanguage            *string    `bun:"reply_language"`
	ContactReadSeq           int64      `bun:"contact_read_seq"`
	ContactNotifiedSeq       int64      `bun:"contact_notified_seq"`
	ContactNotifyDueAt       *time.Time `bun:"contact_notify_due_at"`
}
