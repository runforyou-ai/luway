//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WebSearchSetting 表示企业的联网搜索设置。
type WebSearchSetting struct {
	bun.BaseModel `bun:"table:web_search_settings,alias:wss"`

	OrganizationID string    `bun:"organization_id,pk"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	Provider       string    `bun:"provider"`
	APIKey         string    `bun:"api_key"`
	BaseURL        string    `bun:"base_url"`
}
