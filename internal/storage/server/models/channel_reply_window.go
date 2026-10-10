//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelReplyWindow 保存外部平台允许向渠道身份发送消息的回复窗口。
type ChannelReplyWindow struct {
	bun.BaseModel `bun:"table:channel_reply_windows,alias:crw"`

	ID                string    `bun:"id,pk"`
	WorkspaceID       string    `bun:"workspace_id"`
	ChannelIdentityID string    `bun:"channel_identity_id"`
	Trigger           string    `bun:"trigger"`
	OpenedAt          time.Time `bun:"opened_at"`
	ExpiresAt         time.Time `bun:"expires_at"`
	Quota             *int      `bun:"quota"`
	Used              int       `bun:"used"`
	Reserved          int       `bun:"reserved"`
	CreatedAt         time.Time `bun:"created_at"`
	UpdatedAt         time.Time `bun:"updated_at"`
}
