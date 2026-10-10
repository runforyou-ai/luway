package agentcontract

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Block 定义按模型返回顺序排列的完整中间内容。
type Block struct {
	ID          string                   `json:"id"`
	Position    int64                    `json:"position"`
	ModelCallID string                   `json:"modelCallId"`
	Kind        domain.AgentRunBlockKind `json:"kind"`
	Payload     BlockPayload             `json:"payload"`
}

// BlockPayload 完整保存文本、工具调用参数和结果。
type BlockPayload struct {
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"toolCall,omitempty"`
}

// ToolCall 保存一次模型指定的工具调用，包括可供模型修正的失败。
type ToolCall struct {
	ID          string                 `json:"id"`                 // 调用编号。
	ParentID    string                 `json:"parentId,omitempty"` // 子 Agent 的调用所属的委派调用编号。
	ModelCallID string                 `json:"modelCallId"`        // 发起调用的模型调用编号，子 Agent 的调用取所属委派调用的模型调用编号。
	CallID      string                 `json:"callId"`
	Name        string                 `json:"name"`
	Source      domain.AgentToolSource `json:"source"`
	Replayable  bool                   `json:"replayable"`
	SideEffects bool                   `json:"sideEffects"`
	Arguments   string                 `json:"arguments"` // 模型给出的参数。
	// BoundArguments 是业务系统工具由服务端填入的参数，执行时覆盖同名的模型参数，其他工具为空。
	BoundArguments map[string]string          `json:"boundArguments,omitempty"`
	Result         *string                    `json:"result"`
	Error          *string                    `json:"error"`
	Status         domain.AgentToolCallStatus `json:"status"`
	StartedAt      *time.Time                 `json:"startedAt"`
	CompletedAt    *time.Time                 `json:"completedAt"`
	MCPServer      string                     `json:"mcpServer,omitempty"` // 本机 MCP 工具所属的服务名称，其他来源为空。
	// BusinessSystemID 与 BusinessSystem 是业务系统工具所属的业务系统编号与调用时的名称，其他来源为空。
	BusinessSystemID string `json:"businessSystemId,omitempty"`
	BusinessSystem   string `json:"businessSystem,omitempty"`
	// Level 是业务系统工具与电脑工具调用时推导的操作级别，其他工具为空。
	Level    domain.OperationLevel `json:"level,omitempty"`
	Evidence bool                  `json:"evidence,omitempty"` // 原始结果通过依据判定。
	// Intervention 是提交确认或审批的调用需要的人工介入，提交后调用的状态由裁决与批准后的执行推进。
	Intervention domain.ToolIntervention `json:"intervention,omitempty"`
	Computer     bool                    `json:"-"` // 调用已作为操作派发到电脑，未结束前由电脑领取与上报推进。
	// LocalAgent 是已交给本机 Agent 会话的一轮所委派的本机 Agent 名称，只在从调用记录恢复时取值；这样的调用由会话推进，模型收到已交给本机 Agent 的结果。
	LocalAgent string `json:"-"`
	// Target 是提交确认或审批的电脑工具调用批准后派发的电脑与操作，只在提交时取值。
	Target *ComputerTarget `json:"-"`
	// ComputerOutcome 是电脑上报的结果，只在从调用记录恢复时取值，恢复时据此补回文件内容摘要。
	ComputerOutcome *domain.ComputerOutcome `json:"-"`
	Activity        string                  `json:"-"` // 子 Agent 正在调用的工具名称，只进入运行流。
}

// ComputerTarget 是一次电脑操作的执行电脑与实际执行的操作。
type ComputerTarget struct {
	ComputerID string                   `json:"computerId"`
	Operation  domain.ComputerOperation `json:"operation"`
}

// ProcessChanges 是运行过程自上次成功保存以来的变化：先删除移除的内容块与工具调用，再写入新增或变化的工具调用与内容块。
type ProcessChanges struct {
	Blocks        []Block    // 新增或位置、文本变化的内容块，按位置排列；工具调用块只写入块与调用的对应关系。
	Calls         []ToolCall // 新增或变化的工具调用，包括内容块中的调用与子 Agent 发起的调用。
	RemovedBlocks []string   // 移除的内容块编号。
	RemovedCalls  []string   // 随内容块移除的工具调用编号。
}

// Empty 判断过程是否没有变化。
func (c ProcessChanges) Empty() bool {
	return len(c.Blocks) == 0 && len(c.Calls) == 0 && len(c.RemovedBlocks) == 0 && len(c.RemovedCalls) == 0
}
