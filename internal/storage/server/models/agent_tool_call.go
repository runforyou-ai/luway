//go:build server

package models

import (
	"encoding/json"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// AgentToolCall 保存 Agent 运行中模型发起的一次工具调用。
type AgentToolCall struct {
	bun.BaseModel `bun:"table:agent_tool_calls,alias:atc"`

	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	ID             string    `bun:"id,pk"`
	WorkspaceID    string    `bun:"workspace_id"`
	AgentRunID     string    `bun:"agent_run_id"`
	ParentID       *string   `bun:"parent_id"`
	ModelCallID    string    `bun:"model_call_id"`
	ProviderCallID string    `bun:"provider_call_id"`
	Name           string    `bun:"name"`
	Source         string    `bun:"source"`
	MCPServer      *string   `bun:"mcp_server"`
	// BusinessSystemID 与 BusinessSystemName 是业务系统工具所属的业务系统编号与调用时的名称，其他来源为空。
	BusinessSystemID   *string `bun:"business_system_id"`
	BusinessSystemName *string `bun:"business_system_name"`
	// Level 是业务系统工具与电脑工具调用时推导的操作级别，其他工具为空。
	Level     *string `bun:"level"`
	Arguments string  `bun:"arguments"` // 模型给出的参数。
	// BoundArguments 是服务端按可信上下文填入的参数，执行时覆盖同名的模型参数，没有绑定参数时为空。
	BoundArguments map[string]string `bun:"bound_arguments,type:jsonb,nullzero"`
	Replayable     bool              `bun:"replayable"`
	SideEffects    bool              `bun:"side_effects"`
	Status         string            `bun:"status"`
	Result         *string           `bun:"result"`
	Error          *string           `bun:"error"`
	Evidence       bool              `bun:"evidence"`
	StartedAt      *time.Time        `bun:"started_at"`
	CompletedAt    *time.Time        `bun:"completed_at"`
	// ComputerID 是执行调用的电脑编号，在服务端执行的调用为空。
	ComputerID *string `bun:"computer_id"`
	// Operation 是派发给电脑的操作与电脑上报的结果，仅电脑执行的调用取值。
	Operation *domain.ComputerCall `bun:"operation,type:jsonb"`
	// TraceID 是派发到电脑时日志作用域中的串联编号，执行器执行该操作时沿用；未派发到电脑的调用为空。
	TraceID *string `bun:"trace_id"`
	// Claims 是电脑领取该调用的次数，在服务端执行的调用为 0。
	Claims int `bun:"claims"`
	// Intervention 是执行前需要的人工介入，自动执行的调用为空。
	Intervention *string `bun:"intervention"`
	// AssigneeSubjectID 与 ExpiresAt 是处理确认或审批的聊天主体与截止时间。
	AssigneeSubjectID *string    `bun:"assignee_subject_id"`
	ExpiresAt         *time.Time `bun:"expires_at"`
	// DecidedBySubjectID 与 DecidedAt 是作出确认、审批或核对的聊天主体与时间，过期或取消时主体为空。
	DecidedBySubjectID *string    `bun:"decided_by_subject_id"`
	DecidedAt          *time.Time `bun:"decided_at"`
	// Rev 是运行时对调用记录的修订号。
	Rev uint64 `bun:"rev"`
	// Handover 是调用交给运行外推进的方式，由运行时推进的调用为空。
	Handover *string `bun:"handover"`
	// Payload 是调用交出或提交时运行时给出的载荷。
	Payload json.RawMessage `bun:"payload,type:jsonb,nullzero"`
	// Notes 是工具规格、依据检查与扩展写入的调用注解。
	Notes map[string]string `bun:"notes,type:jsonb,nullzero"`
	// Media 是调用结果附带的媒体引用。
	Media json.RawMessage `bun:"media,type:jsonb,nullzero"`
	// Decision 是暂停运行等待确认的调用的决定，提交给处理人的调用为空。
	Decision json.RawMessage `bun:"decision,type:jsonb,nullzero"`
	// Completion 是结束运行的调用给出的完成载荷。
	Completion json.RawMessage `bun:"completion,type:jsonb,nullzero"`
	// LocalAgentSessionID 是委派给本机 Agent 的一轮或其权限请求所属的本机 Agent 会话编号，非空时调用由会话推进，不随运行结束。
	LocalAgentSessionID *string `bun:"local_agent_session_id"`
}
