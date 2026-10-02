//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ContactChannelIdentity 表示联系人在消息渠道中的身份。
type ContactChannelIdentity struct {
	bun.BaseModel `bun:"table:contact_channel_identities,alias:cci"`

	ID              string     `bun:"id,pk"`
	OrganizationID  string     `bun:"organization_id"`
	ContactID       string     `bun:"contact_id"`
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
