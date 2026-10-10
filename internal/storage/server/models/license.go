//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// License 表示平台当前生效的授权。
type License struct {
	bun.BaseModel `bun:"table:licenses,alias:lic"`

	ServerID         string          `bun:"server_id,pk"`
	CreatedAt        time.Time       `bun:"created_at,nullzero,default:now()"`
	UpdatedAt        time.Time       `bun:"updated_at,nullzero,default:now()"`
	LicenseID        string          `bun:"license_id"`
	Customer         string          `bun:"customer"`
	LicenseCode      string          `bun:"license_code"`
	Capabilities     json.RawMessage `bun:"capabilities,type:jsonb"`
	IssuedAt         time.Time       `bun:"issued_at"`
	ExpiresAt        time.Time       `bun:"expires_at"`
	ControlMissingAt *time.Time      `bun:"control_missing_at"`
}
