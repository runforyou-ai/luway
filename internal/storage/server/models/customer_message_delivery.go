//go:build server

package models

import (
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
	"time"
)

// CustomerMessageDelivery 保存客户消息的外发顺序与平台投递事实。
type CustomerMessageDelivery struct {
	bun.BaseModel            `bun:"table:customer_message_deliveries,alias:cmd"`
	ID                       string                        `bun:"id,pk"`
	OrganizationID           string                        `bun:"organization_id"`
	ConversationID           string                        `bun:"conversation_id"`
	MessageID                string                        `bun:"message_id"`
	ChannelID                string                        `bun:"channel_id"`
	ContactChannelIdentityID string                        `bun:"contact_channel_identity_id"`
	BotID                    int64                         `bun:"bot_id"`
	Position                 int64                         `bun:"position"`
	Status                   domain.CustomerDeliveryStatus `bun:"status"`
	Attempt                  int                           `bun:"attempt"`
	ReplyProviderMessageID   *string                       `bun:"reply_provider_message_id"`
	ProviderMessageID        *int64                        `bun:"provider_message_id"`
	LeaseWorker              *string                       `bun:"lease_worker"`
	LeaseExpiresAt           *time.Time                    `bun:"lease_expires_at"`
	AvailableAt              time.Time                     `bun:"available_at"`
	UncertainUntil           *time.Time                    `bun:"uncertain_until"`
	LastError                string                        `bun:"last_error"`
	SentAt                   *time.Time                    `bun:"sent_at"`
	CreatedAt                time.Time                     `bun:"created_at"`
	UpdatedAt                time.Time                     `bun:"updated_at"`
}
