//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ContactTag 表示企业自定义的联系人标签。
type ContactTag struct {
	bun.BaseModel `bun:"table:contact_tags,alias:ctg"`

	ID             string    `bun:"id,pk"`
	OrganizationID string    `bun:"organization_id"`
	Name           string    `bun:"name"`
	AIInstruction  string    `bun:"ai_instruction"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
}
