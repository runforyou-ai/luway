//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelMessage 保存平台消息身份与入站引用的一层快照。
type ChannelMessage struct {
	bun.BaseModel `bun:"table:channel_messages,alias:cm"`

	CreatedAt              time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt              time.Time `bun:"updated_at,nullzero,default:now()"`
	MessageID              string    `bun:"message_id,pk"`
	OrganizationID         string    `bun:"organization_id"`
	ConversationID         string    `bun:"conversation_id"`
	ChannelID              string    `bun:"channel_id"`
	ProviderAccountID      string    `bun:"provider_account_id"`
	ProviderConversationID string    `bun:"provider_conversation_id"`
	ProviderMessageID      string    `bun:"provider_message_id"`
	ReplyProviderMessageID *string   `bun:"reply_provider_message_id"`
	ReplyBody              string    `bun:"reply_body"`
	ReplySenderName        string    `bun:"reply_sender_name"`
	ReplySenderIsBot       bool      `bun:"reply_sender_is_bot"`
}
