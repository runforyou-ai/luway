//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// RolePermission 表示 PostgreSQL 中的角色权限关联。
type RolePermission struct {
	bun.BaseModel `bun:"table:role_permissions,alias:rp"`

	OrganizationID string    `bun:"organization_id"`
	RoleID         string    `bun:"role_id,pk"`
	Permission     string    `bun:"permission,pk"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
}
