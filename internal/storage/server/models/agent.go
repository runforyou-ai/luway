//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// Agent 表示 PostgreSQL 中的 AI 员工或助理，类型以企业身份为准。
type Agent struct {
	bun.BaseModel `bun:"table:agents,alias:a"`

	ID                string                   `bun:"id,pk"`
	IdentityID        string                   `bun:"identity_id"`
	OrganizationID    string                   `bun:"organization_id"`
	ActiveRevisionID  string                   `bun:"active_revision_id"`
	Status            string                   `bun:"status"`
	OwnerUserID       *string                  `bun:"owner_user_id"`
	DeviceID          *string                  `bun:"device_id"`
	ServiceAudiences  []domain.ServiceAudience `bun:"service_audiences,array"`
	HandoffTeamID     *string                  `bun:"handoff_team_id"`
	ResponsibleUserID *string                  `bun:"responsible_user_id"`
	PausedAt          *time.Time               `bun:"paused_at"`
	CreatedAt         time.Time                `bun:"created_at"`
	UpdatedAt         time.Time                `bun:"updated_at"`
}
