//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelIdentity 表示外部平台账号在渠道中的身份，服务客户的渠道归属联系人，服务成员的渠道绑定成员。
type ChannelIdentity struct {
	bun.BaseModel `bun:"table:channel_identities,alias:ci"`

	ID              string     `bun:"id,pk"`
	WorkspaceID     string     `bun:"workspace_id"`
	ContactID       *string    `bun:"contact_id"`
	UserIdentityID  *string    `bun:"user_identity_id"`
	ChannelID       string     `bun:"channel_id"`
	ExternalID      string     `bun:"external_id"`
	VerifiedUserID  *string    `bun:"verified_user_id"`
	DisplayName     *string    `bun:"display_name"`
	AvatarFileID    *string    `bun:"avatar_file_id"`
	AvatarCheckedAt *time.Time `bun:"avatar_checked_at"`
	CreatedAt       time.Time  `bun:"created_at"`
	UpdatedAt       time.Time  `bun:"updated_at"`
	LastSeenAt      *time.Time `bun:"last_seen_at"`
}
