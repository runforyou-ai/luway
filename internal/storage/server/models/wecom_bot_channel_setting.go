//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WeComBotChannelSetting 表示企业微信智能机器人渠道的长连接凭据。
type WeComBotChannelSetting struct {
	bun.BaseModel `bun:"table:wecom_bot_channel_settings,alias:wbs"`

	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
	ChannelID   string    `bun:"channel_id,pk"`
	WorkspaceID string    `bun:"workspace_id"`
	Secret      string    `bun:"secret"`
}
