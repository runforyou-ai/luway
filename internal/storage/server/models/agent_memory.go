//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AgentMemory 表示仅服务负责人本人的 AI 员工的一条长期记忆。
type AgentMemory struct {
	bun.BaseModel `bun:"table:agent_memories,alias:am"`

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
