//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// TeamMember 表示 PostgreSQL 中的团队成员关系。
type TeamMember struct {
	bun.BaseModel `bun:"table:team_members,alias:tm"`

	ID              string    `bun:"id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	TeamID          string    `bun:"team_id"`
	IdentityID      string    `bun:"identity_id"`
	CreatedByUserID string    `bun:"created_by_user_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at,nullzero,default:now()"`
}
