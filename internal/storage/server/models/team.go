//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Team 表示 PostgreSQL 中的企业团队。
type Team struct {
	bun.BaseModel `bun:"table:teams,alias:t"`

	ID              string    `bun:"id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	Name            string    `bun:"name"`
	Description     string    `bun:"description"`
	CreatedByUserID string    `bun:"created_by_user_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}
