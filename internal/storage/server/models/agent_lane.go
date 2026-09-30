//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AgentLane 表示 Agent 在一个执行范围内的输入队列与水位。
type AgentLane struct {
	bun.BaseModel `bun:"table:agent_lanes,alias:al"`

	ID              string    `bun:"id,pk"`
	OrganizationID  string    `bun:"organization_id"`
	ConversationID  string    `bun:"conversation_id"`
	AgentIdentityID string    `bun:"agent_identity_id"`
	ScopeKind       string    `bun:"scope_kind"`
	ScopeID         string    `bun:"scope_id"`
	DesiredSeq      int64     `bun:"desired_seq"`
	ProcessedSeq    int64     `bun:"processed_seq"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}
