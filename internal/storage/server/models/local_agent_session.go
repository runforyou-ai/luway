//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// LocalAgentSession 表示 AI 员工在会话中委派给电脑上本机 Agent 的会话。
type LocalAgentSession struct {
	bun.BaseModel `bun:"table:local_agent_sessions,alias:las"`

	ID             string    `bun:"id,pk"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	WorkspaceID    string    `bun:"workspace_id"`
	ConversationID string    `bun:"conversation_id"`
	AgentID        string    `bun:"agent_id"`
	ComputerID     string    `bun:"computer_id"`
	LocalAgent     string    `bun:"local_agent"`
	// SessionID 是本机 Agent 返回的 ACP 会话编号，首轮完成前为空。
	SessionID *string `bun:"session_id"`
	Status    string  `bun:"status"`
}

// AgentToolCallUpdate 表示电脑执行工具调用期间上报的一条过程更新。
type AgentToolCallUpdate struct {
	bun.BaseModel `bun:"table:agent_tool_call_updates,alias:atcu"`

	ToolCallID  string                `bun:"tool_call_id,pk"`
	Seq         int                   `bun:"seq,pk"`
	CreatedAt   time.Time             `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time             `bun:"updated_at,nullzero,default:now()"`
	WorkspaceID string                `bun:"workspace_id"`
	Content     domain.ToolCallUpdate `bun:"content,type:jsonb"`
}
