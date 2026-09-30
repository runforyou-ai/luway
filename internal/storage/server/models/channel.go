//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Channel 表示企业接收客户消息的渠道。
type Channel struct {
	bun.BaseModel `bun:"table:channels,alias:c"`

	ID                        string    `bun:"id,pk"`
	OrganizationID            string    `bun:"organization_id"`
	CreatedByUserID           string    `bun:"created_by_user_id"`
	Type                      string    `bun:"type"`
	Name                      string    `bun:"name"`
	Description               *string   `bun:"description"`
	DefaultLocale             string    `bun:"default_locale"`
	InitialRoutingTargetType  string    `bun:"initial_routing_target_type"`
	InitialRoutingTargetID    *string   `bun:"initial_routing_target_id"`
	FallbackRoutingTargetType string    `bun:"fallback_routing_target_type"`
	FallbackRoutingTargetID   *string   `bun:"fallback_routing_target_id"`
	Enabled                   bool      `bun:"enabled"`
	CreatedAt                 time.Time `bun:"created_at"`
	UpdatedAt                 time.Time `bun:"updated_at"`
}
