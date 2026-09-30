//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// TelegramChannelSetting 表示 Telegram 渠道的机器人和 Webhook 设置。
type TelegramChannelSetting struct {
	bun.BaseModel `bun:"table:telegram_channel_settings,alias:tcs"`

	ChannelID          string     `bun:"channel_id,pk"`
	OrganizationID     string     `bun:"organization_id"`
	BotToken           *string    `bun:"bot_token"`
	BotID              *int64     `bun:"bot_id"`
	BotUsername        *string    `bun:"bot_username"`
	BotDisplayName     *string    `bun:"bot_display_name"`
	WebhookBaseURL     *string    `bun:"webhook_base_url"`
	WebhookSecret      *string    `bun:"webhook_secret"`
	WebhookStatus      *string    `bun:"webhook_status"`
	WebhookConnectedAt *time.Time `bun:"webhook_connected_at"`
	CreatedAt          time.Time  `bun:"created_at"`
	UpdatedAt          time.Time  `bun:"updated_at"`
}
