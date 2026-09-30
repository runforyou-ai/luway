//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Role 表示 PostgreSQL 中的企业角色。
type Role struct {
	bun.BaseModel `bun:"table:roles,alias:r"`

	ID             string    `bun:"id,pk"`
	OrganizationID string    `bun:"organization_id"`
	Kind           string    `bun:"kind"`
	Name           string    `bun:"name"`
	Description    string    `bun:"description"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
}
