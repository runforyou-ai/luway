//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WebFetchSetting 表示企业的网页读取设置。
type WebFetchSetting struct {
	bun.BaseModel `bun:"table:web_fetch_settings,alias:wfs"`

	WorkspaceID string    `bun:"workspace_id,pk"`
	CreatedAt   time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time `bun:"updated_at,nullzero,default:now()"`
	Provider    string    `bun:"provider"`
	APIKey      string    `bun:"api_key"`
	BaseURL     string    `bun:"base_url"`
}
