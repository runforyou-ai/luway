//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AssistantMemory 表示助理的一条长期记忆。
type AssistantMemory struct {
	bun.BaseModel `bun:"table:assistant_memories,alias:am"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
	OrganizationID string    `bun:"organization_id"`
	AgentID        string    `bun:"agent_id"`
	Path           string    `bun:"path"`
	Name           string    `bun:"name"`
	Description    string    `bun:"description"`
	Body           string    `bun:"body"`
}
