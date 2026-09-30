//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// OrganizationEntitlement 表示企业当前生效的服务权益快照。
type OrganizationEntitlement struct {
	bun.BaseModel `bun:"table:organization_entitlements,alias:oe"`

	OrganizationID string     `bun:"organization_id,pk"`
	Revision       int64      `bun:"revision"`
	PlanCode       string     `bun:"plan_code"`
	ServiceEndsAt  *time.Time `bun:"service_ends_at"`
	AppliedAt      time.Time  `bun:"applied_at"`
	CreatedAt      time.Time  `bun:"created_at"`
	UpdatedAt      time.Time  `bun:"updated_at"`
}
