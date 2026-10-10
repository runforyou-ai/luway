//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Contact 表示企业的外部联系人。
type Contact struct {
	bun.BaseModel `bun:"table:contacts,alias:ct"`

	ID              string     `bun:"id,pk"`
	WorkspaceID     string     `bun:"workspace_id"`
	Number          int64      `bun:"number"`
	CreatedByUserID *string    `bun:"created_by_user_id"`
	SourceChannelID string     `bun:"source_channel_id"`
	DisplayName     *string    `bun:"display_name"`
	PrimaryEmail    *string    `bun:"primary_email"`
	Stage           string     `bun:"stage"`
	Notes           *string    `bun:"notes"`
	ExternalUserID  *string    `bun:"external_user_id"`
	CreatedAt       time.Time  `bun:"created_at"`
	UpdatedAt       time.Time  `bun:"updated_at"`
	DeletedAt       *time.Time `bun:"deleted_at"`
}
