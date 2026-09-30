//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServiceCategory 表示 PostgreSQL 中的企业咨询分类。
type ServiceCategory struct {
	bun.BaseModel `bun:"table:service_categories,alias:sc"`

	ID             string     `bun:"id,pk"`
	OrganizationID string     `bun:"organization_id"`
	Name           string     `bun:"name"`
	Description    string     `bun:"description"`
	TeamID         *string    `bun:"team_id"`
	ArchivedAt     *time.Time `bun:"archived_at"`
	CreatedAt      time.Time  `bun:"created_at"`
	UpdatedAt      time.Time  `bun:"updated_at"`
}
