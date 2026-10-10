//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChannelBindingToken 表示把渠道身份绑定到成员的一次性令牌。
type ChannelBindingToken struct {
	bun.BaseModel `bun:"table:channel_binding_tokens,alias:cbt"`

	CreatedAt         time.Time  `bun:"created_at"`
	UpdatedAt         time.Time  `bun:"updated_at,nullzero,default:now()"`
	ID                string     `bun:"id,pk"`
	WorkspaceID       string     `bun:"workspace_id"`
	ChannelIdentityID string     `bun:"channel_identity_id"`
	TokenHash         string     `bun:"token_hash"`
	ExpiresAt         time.Time  `bun:"expires_at"`
	DeliveredAt       *time.Time `bun:"delivered_at"`
	UsedAt            *time.Time `bun:"used_at"`
}
