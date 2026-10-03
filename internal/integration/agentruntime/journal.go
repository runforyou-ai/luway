package agentruntime

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// ErrAwaitExternal 由需要等待外部结果的工具返回：调用记为等待，本批工具结束后运行保存恢复状态并挂起。
var ErrAwaitExternal = errors.New("agent tool call awaits an external result")

// errRunSuspended 表示运行在本批工具结束后挂起。
var errRunSuspended = errors.New("agent run suspended")

// Journal 由执行侧实现，在安全点持久化运行过程与恢复状态。
type Journal interface {
	// SaveStep 在一次模型输出定稿后原子写入全部内容块、子 Agent 调用、用量、任务清单与恢复状态。
	SaveStep(ctx context.Context, step Step) error
	// SaveToolCall 写入一次工具调用的当前状态，已有最终结果的调用保持不变。
	SaveToolCall(ctx context.Context, call ToolCall) error
}

// Step 是运行在安全点的完整过程与恢复状态。
type Step struct {
	Blocks []Block
	Calls  []ToolCall
	Usage  Usage
	Plan   []runstream.PlanTask
	State  []byte
}

// Resume 是继续执行一次运行所需的已保存内容：恢复状态，以及已写入的内容块、子 Agent 调用与任务清单。
type Resume struct {
	State  []byte
	Blocks []Block
	Calls  []ToolCall
	Plan   []runstream.PlanTask
}

// checkpoint 是运行在安全点的恢复状态，以 gob 编码保存，消息扩展类型由各模型组件注册。
type checkpoint struct {
	Messages     []*schema.AgenticMessage // 最近一次模型输出定稿时的模型上下文，不含系统指令。
	Seen         map[string]struct{}      // 已进入上下文的会话消息编号与修订。
	MediaCount   int
	MediaBytes   int64
	ClaimedSeq   int64
	Turns        int
	MediaEnabled bool
	KeepFromID   string // 摘要保留点的消息编号。
	Iterations   int    // 当前轮次已进行的模型规划次数。
	Terminal     terminalState
	Evidence     map[string]string // 当前输入边界内通过依据判定的工具调用编号及其原始结果。
	Grounded     bool
	PlanFiles    map[string]string
	Offloaded    map[string]string // 转存的大体积工具结果，按文件路径索引。
	Usage        Usage
}

// terminalState 是终止工具在安全点的纠正额度与已登记意图。
type terminalState struct {
	Corrections int
	Intents     map[string]terminalIntentState
	Forced      *terminalIntentState
	Handoff     bool
}

// terminalIntentState 是一次终止意图的可编码形式。
type terminalIntentState struct {
	Decision TerminalDecision
	Message  string
}

// encodeCheckpoint 以 gob 编码恢复状态。
func encodeCheckpoint(state checkpoint) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(state); err != nil {
		return nil, fmt.Errorf("encode agent run checkpoint: %w", err)
	}
	return buffer.Bytes(), nil
}

// decodeCheckpoint 解码恢复状态。
func decodeCheckpoint(data []byte) (checkpoint, error) {
	var state checkpoint
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return checkpoint{}, fmt.Errorf("decode agent run checkpoint: %w", err)
	}
	return state, nil
}

// 附加信息中的常见取值按相同类型编解码。
func init() {
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

type modelCallIDContextKey struct{}

// ModelCallID 返回 Runtime 为当前模型调用分配的编号，模型组件以它作为调用记录编号；不在 Runtime 模型调用中时为空。
func ModelCallID(ctx context.Context) string {
	id, _ := ctx.Value(modelCallIDContextKey{}).(string)
	return id
}
