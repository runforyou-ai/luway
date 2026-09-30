//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// Device 表示 PostgreSQL 中成员注册的本机设备。
type Device struct {
	bun.BaseModel `bun:"table:devices,alias:d"`

	ID             string                  `bun:"id,pk"`
	OrganizationID string                  `bun:"organization_id"`
	UserID         string                  `bun:"user_id"`
	InstallID      string                  `bun:"install_id"`
	Name           string                  `bun:"name"`
	Platform       domain.DevicePlatform   `bun:"platform"`
	RevokedAt      *time.Time              `bun:"revoked_at"`
	WorkSeq        int64                   `bun:"work_seq"`
	LastSeenAt     *time.Time              `bun:"last_seen_at"`
	LocalAgents    []domain.LocalAgentKind `bun:"local_agents,type:jsonb"`
	CreatedAt      time.Time               `bun:"created_at"`
	UpdatedAt      time.Time               `bun:"updated_at"`
}
