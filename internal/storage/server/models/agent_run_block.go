//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// AgentRunBlock 保存 Agent 运行的一个完整中间内容块。
type AgentRunBlock struct {
	bun.BaseModel `bun:"table:agent_run_blocks,alias:arb"`

	CreatedAt   time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time `bun:"updated_at,nullzero,default:now()"`
	ID          string    `bun:"id,pk"`
	WorkspaceID string    `bun:"workspace_id"`
	AgentRunID  string    `bun:"agent_run_id"`
	Position    int64     `bun:"position"`
	ModelCallID string    `bun:"model_call_id"`
	Kind        string    `bun:"kind"`
	Content     *string   `bun:"content"`
	ToolCallID  *string   `bun:"tool_call_id"`
}
