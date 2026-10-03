//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// Platform 表示 PostgreSQL 中唯一一行的平台记录与平台级策略。
type Platform struct {
	bun.BaseModel `bun:"table:platforms,alias:pf"`

	ServerID                 string    `bun:"server_id,pk"`
	CreatedAt                time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt                time.Time `bun:"updated_at,nullzero,default:now()"`
	RegistrationPolicy       string    `bun:"registration_policy"`
	WorkspaceCreationPolicy  string    `bun:"workspace_creation_policy"`
	TimeZone                 string    `bun:"time_zone"`
	StatisticsRebuildPending bool      `bun:"statistics_rebuild_pending"`
	ServerPrivateKey         []byte    `bun:"server_private_key"`
	TelemetryEnabled         bool      `bun:"telemetry_enabled"`
	DailyCreditGrant         int64     `bun:"daily_credit_grant"`
}
