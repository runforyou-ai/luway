//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// CreditLot 表示 PostgreSQL 中的积分批次。
type CreditLot struct {
	bun.BaseModel `bun:"table:credit_lots,alias:cl"`

	ID             string     `bun:"id,pk"`
	CreatedAt      time.Time  `bun:"created_at,nullzero,default:now()"`
	OrganizationID string     `bun:"organization_id"`
	Source         string     `bun:"source"`
	AdjustmentID   *string    `bun:"adjustment_id"`
	GrantDate      *string    `bun:"grant_date"`
	Amount         int64      `bun:"amount"`
	Remaining      int64      `bun:"remaining"`
	ExpiresAt      *time.Time `bun:"expires_at"`
}

// CreditAdjustment 表示 PostgreSQL 中平台管理员对工作区积分的手动调整。
type CreditAdjustment struct {
	bun.BaseModel `bun:"table:credit_adjustments,alias:ca"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	OrganizationID string    `bun:"organization_id"`
	AccountID      string    `bun:"account_id"`
	Amount         int64     `bun:"amount"`
	Note           string    `bun:"note"`
}

// CreditMovement 表示 PostgreSQL 中的积分批次变动。
type CreditMovement struct {
	bun.BaseModel `bun:"table:credit_movements,alias:cm"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	OrganizationID string    `bun:"organization_id"`
	LotID          string    `bun:"lot_id"`
	Amount         int64     `bun:"amount"`
	SourceType     string    `bun:"source_type"`
	SourceID       string    `bun:"source_id"`
}
