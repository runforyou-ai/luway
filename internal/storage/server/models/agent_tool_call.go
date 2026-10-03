//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// AgentToolCall 保存 Agent 运行中模型发起的一次工具调用。
type AgentToolCall struct {
	bun.BaseModel `bun:"table:agent_tool_calls,alias:atc"`

	CreatedAt      time.Time  `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time  `bun:"updated_at,nullzero,default:now()"`
	ID             string     `bun:"id,pk"`
	OrganizationID string     `bun:"organization_id"`
	AgentRunID     string     `bun:"agent_run_id"`
	ParentID       *string    `bun:"parent_id"`
	ModelCallID    string     `bun:"model_call_id"`
	ProviderCallID string     `bun:"provider_call_id"`
	Name           string     `bun:"name"`
	Source         string     `bun:"source"`
	MCPServer      *string    `bun:"mcp_server"`
	Arguments      string     `bun:"arguments"`
	Replayable     bool       `bun:"replayable"`
	SideEffects    bool       `bun:"side_effects"`
	Status         string     `bun:"status"`
	Result         *string    `bun:"result"`
	Error          *string    `bun:"error"`
	Evidence       bool       `bun:"evidence"`
	StartedAt      *time.Time `bun:"started_at"`
	CompletedAt    *time.Time `bun:"completed_at"`
	// ComputerID 是执行调用的电脑编号，在服务端执行的调用为空。
	ComputerID *string `bun:"computer_id"`
	// Operation 是派发给电脑的操作与电脑上报的结果，仅电脑执行的调用取值。
	Operation *domain.ComputerCall `bun:"operation,type:jsonb"`
}
