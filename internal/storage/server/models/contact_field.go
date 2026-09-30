//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ContactField 表示企业自定义的联系人字段。
type ContactField struct {
	bun.BaseModel `bun:"table:contact_fields,alias:cf"`

	ID             string                      `bun:"id,pk"`
	OrganizationID string                      `bun:"organization_id"`
	Name           string                      `bun:"name"`
	Type           string                      `bun:"type"`
	Options        []domain.ContactFieldOption `bun:"options,type:jsonb"`
	AIInstruction  string                      `bun:"ai_instruction"`
	CreatedAt      time.Time                   `bun:"created_at"`
	UpdatedAt      time.Time                   `bun:"updated_at"`
}
