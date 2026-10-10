//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ContactFieldValue 表示联系人的自定义字段取值。
type ContactFieldValue struct {
	bun.BaseModel `bun:"table:contact_field_values,alias:cfv"`

	ID                     string     `bun:"id,pk"`
	WorkspaceID            string     `bun:"workspace_id"`
	ContactID              string     `bun:"contact_id"`
	FieldID                string     `bun:"field_id"`
	Value                  string     `bun:"value"`
	Source                 string     `bun:"source"`
	SourceUserID           *string    `bun:"source_user_id"`
	SourceServiceSessionID *string    `bun:"source_service_session_id"`
	SourceSessionClosedAt  *time.Time `bun:"source_session_closed_at"`
	CreatedAt              time.Time  `bun:"created_at"`
	UpdatedAt              time.Time  `bun:"updated_at"`
}
