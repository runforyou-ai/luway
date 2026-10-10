//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelDeliveryItem 保存一条渠道消息投递中按平台请求拆分的有序请求项。
type ChannelDeliveryItem struct {
	bun.BaseModel `bun:"table:channel_delivery_items,alias:cdi"`

	DeliveryID        string     `bun:"delivery_id,pk"`
	Seq               int        `bun:"seq,pk"`
	WorkspaceID       string     `bun:"workspace_id"`
	Body              string     `bun:"body"`
	Attachment        bool       `bun:"attachment"`
	ProviderMessageID *string    `bun:"provider_message_id"`
	SentAt            *time.Time `bun:"sent_at"`
	CreatedAt         time.Time  `bun:"created_at"`
	UpdatedAt         time.Time  `bun:"updated_at"`
}
