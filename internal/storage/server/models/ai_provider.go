//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AIProvider 表示 PostgreSQL 中的模型服务供应商。
type AIProvider struct {
	bun.BaseModel `bun:"table:ai_providers,alias:aip"`

	ID             string    `bun:"id,pk"`
	OrganizationID string    `bun:"organization_id"`
	Brand          string    `bun:"brand"`
	Name           string    `bun:"name"`
	CredentialType string    `bun:"credential_type"`
	APIKey         string    `bun:"api_key"`
	APIURL         string    `bun:"api_url"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
}
