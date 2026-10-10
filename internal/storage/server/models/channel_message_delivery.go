//go:build server

package models

import (
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
	"time"
)

// ChannelMessageDelivery 保存渠道消息的外发顺序与平台投递事实。
type ChannelMessageDelivery struct {
	bun.BaseModel          `bun:"table:channel_message_deliveries,alias:cmd"`
	ID                     string                       `bun:"id,pk"`
	WorkspaceID            string                       `bun:"workspace_id"`
	ConversationID         string                       `bun:"conversation_id"`
	MessageID              string                       `bun:"message_id"`
	ChannelID              string                       `bun:"channel_id"`
	ChannelIdentityID      string                       `bun:"channel_identity_id"`
	ProviderAccountID      string                       `bun:"provider_account_id"`
	Position               int64                        `bun:"position"`
	Status                 domain.ChannelDeliveryStatus `bun:"status"`
	Attempt                int                          `bun:"attempt"`
	ReplyProviderMessageID *string                      `bun:"reply_provider_message_id"`
	ReplyWindowID          *string                      `bun:"reply_window_id"`
	LeaseWorker            *string                      `bun:"lease_worker"`
	LeaseExpiresAt         *time.Time                   `bun:"lease_expires_at"`
	ClaimedAt              *time.Time                   `bun:"claimed_at"`
	AvailableAt            time.Time                    `bun:"available_at"`
	UncertainUntil         *time.Time                   `bun:"uncertain_until"`
	LastError              string                       `bun:"last_error"`
	SentAt                 *time.Time                   `bun:"sent_at"`
	CreatedAt              time.Time                    `bun:"created_at"`
	UpdatedAt              time.Time                    `bun:"updated_at"`
}
