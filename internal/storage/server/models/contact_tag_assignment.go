//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ContactTagAssignment 表示联系人上的一个标签。
type ContactTagAssignment struct {
	bun.BaseModel `bun:"table:contact_tag_assignments,alias:cta"`

	ID                     string    `bun:"id,pk"`
	WorkspaceID            string    `bun:"workspace_id"`
	ContactID              string    `bun:"contact_id"`
	TagID                  string    `bun:"tag_id"`
	Source                 string    `bun:"source"`
	SourceUserID           *string   `bun:"source_user_id"`
	SourceServiceSessionID *string   `bun:"source_service_session_id"`
	CreatedAt              time.Time `bun:"created_at"`
	UpdatedAt              time.Time `bun:"updated_at"`
}
