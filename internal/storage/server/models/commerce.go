//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// CommercePairing 表示 PostgreSQL 中平台与商业服务的配对。
type CommercePairing struct {
	bun.BaseModel `bun:"table:commerce_pairings,alias:cp"`

	ServerID       string     `bun:"server_id,pk"`
	CreatedAt      time.Time  `bun:"created_at,nullzero,default:now()"`
	URL            string     `bun:"url"`
	ServiceID      string     `bun:"service_id"`
	PublicKey      []byte     `bun:"public_key"`
	ChangeSequence int64      `bun:"change_sequence"`
	SyncedAt       *time.Time `bun:"synced_at"`
	FailedAt       *time.Time `bun:"failed_at"`
	Failure        string     `bun:"failure"`
}

// WorkspaceEntitlement 表示 PostgreSQL 中商业服务为工作区生成的当前生效权益。
type WorkspaceEntitlement struct {
	bun.BaseModel `bun:"table:workspace_entitlements,alias:we"`

	OrganizationID string     `bun:"organization_id,pk"`
	UpdatedAt      time.Time  `bun:"updated_at,nullzero,default:now()"`
	Revision       int64      `bun:"revision"`
	PlanID         string     `bun:"plan_id"`
	PlanName       string     `bun:"plan_name"`
	SeatLimit      int        `bun:"seat_limit"`
	PeriodEnd      *time.Time `bun:"period_end"`
}
