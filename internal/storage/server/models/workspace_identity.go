//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WorkspaceIdentity 表示 PostgreSQL 中的企业身份。
type WorkspaceIdentity struct {
	bun.BaseModel `bun:"table:workspace_identities,alias:oi"`

	ID                     string    `bun:"id,pk"`
	WorkspaceID            string    `bun:"workspace_id"`
	Type                   string    `bun:"type"`
	DisplayName            string    `bun:"display_name"`
	AvatarFileID           *string   `bun:"avatar_file_id"`
	HandlesServiceRequests bool      `bun:"handles_service_requests"`
	WorkStatus             string    `bun:"work_status"`
	WorkStatusUpdatedAt    time.Time `bun:"work_status_updated_at"`
	CreatedAt              time.Time `bun:"created_at"`
	UpdatedAt              time.Time `bun:"updated_at"`
}
