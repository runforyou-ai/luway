//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ChatSubject 表示企业聊天主体。
type ChatSubject struct {
	bun.BaseModel `bun:"table:chat_subjects,alias:cs"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
	OrganizationID string    `bun:"organization_id"`
	Kind           string    `bun:"kind"`
	SourceID       string    `bun:"source_id"`
}
