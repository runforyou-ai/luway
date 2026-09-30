//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ContactMethod 表示外部联系人的邮箱或电话。
type ContactMethod struct {
	bun.BaseModel `bun:"table:contact_methods,alias:cm"`

	ID              string    `bun:"id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	ContactID       string    `bun:"contact_id"`
	Type            string    `bun:"type"`
	Value           string    `bun:"value"`
	NormalizedValue string    `bun:"normalized_value"`
	Label           *string   `bun:"label"`
	IsPrimary       bool      `bun:"is_primary"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}
