package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

const (
	// browserToolName 是在电脑的浏览器中执行动作的工具名称。
	browserToolName = "browser"
	// desktopToolName 是在电脑桌面上执行动作的工具名称。
	desktopToolName = "desktop"
)

// browserToolDesc 是浏览器工具的说明。
const browserToolDesc = `在电脑的浏览器中执行一个动作，同一会话共用一个浏览器上下文，同时进行的子任务的浏览器动作可能穿插执行。
- action 指定动作，arguments 是该动作参数的 JSON 对象。
- 每次只调用一个动作，拿到结果后再决定下一步。`

// desktopToolDesc 是桌面工具的说明。
const desktopToolDesc = `在电脑桌面上执行一个动作，整台电脑共用一个桌面，其他会话的桌面动作可能穿插执行。
- action 指定动作，arguments 是该动作参数的 JSON 对象。
- 每次只调用一个动作，拿到结果后再决定下一步。`

// errInterfaceAwaiting 是同一批中前一个界面调用仍在等待结果时，后续界面调用交给模型的失败原因。
var errInterfaceAwaiting = errors.New("前一个浏览器或桌面动作仍在执行，这个动作没有执行。拿到前一个动作的结果后再调用。")

// interfaceToolArgs 是浏览器与桌面工具的参数。
type interfaceToolArgs struct {
	Action    string          `json:"action"`
	Arguments json.RawMessage `json:"arguments"`
}

// newInterfaceTool 创建浏览器或桌面工具，action 参数的取值为该类操作支持的全部动作，arguments 须是 JSON 对象，未提供时按空对象派发。
func newInterfaceTool[A ~string](kind domain.ComputerOperationKind, name, desc string, actions []A) (*computerTool, error) {
	enum := arr.Map(actions, func(action A) any { return string(action) })
	definition, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"action"},
		"properties": map[string]any{
			"action":    map[string]any{"type": "string", "enum": enum, "description": "要执行的动作"},
			"arguments": map[string]any{"type": "object", "description": "动作的参数"},
		},
	})
	if err != nil {
		return nil, err
	}
	parameters := &jsonschema.Schema{}
	if err := json.Unmarshal(definition, parameters); err != nil {
		return nil, err
	}
	return &computerTool{
		info: &schema.ToolInfo{Name: name, Desc: desc, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameters)},
		operation: func(argumentsInJSON string) (domain.ComputerOperation, error) {
			var input interfaceToolArgs
			if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
				return domain.ComputerOperation{}, errors.New("参数不是合法 JSON，请重新提交。")
			}
			arguments := "{}"
			if len(input.Arguments) > 0 {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(input.Arguments, &object); err != nil || object == nil {
					return domain.ComputerOperation{}, errors.New("arguments 必须是 JSON 对象，请重新提交。")
				}
				arguments = string(input.Arguments)
			}
			return domain.ComputerOperation{Kind: kind, Action: input.Action, Arguments: arguments}, nil
		},
	}, nil
}

// interfaceSequencerName 是界面调用排序扩展的名称。
const interfaceSequencerName = "luway.interface_sequencer"

// interfaceSequencer 让每个 Agent 同一批工具调用中的浏览器与桌面调用按模型给出的顺序逐个执行，其余工具照常并行；前一个界面调用挂起等待结果后，本批其余界面调用不派发。
// 它作为运行扩展观察每次模型输出，并在工具执行的最外层放行界面调用；各 Agent 按 AgentScope.ID 分别排序，一个 Agent 的模型调用依次进行，同一时刻只有一批工具调用。
type interfaceSequencer struct {
	mu     sync.Mutex
	agents map[string]*interfaceBatch
}

// interfaceBatch 是一个 Agent 当前一批工具调用中界面调用的顺序。
type interfaceBatch struct {
	// turns 是本批每个界面调用的开始信号，按模型调用标识登记，前一个界面调用结束时关闭下一个的信号。
	turns map[string]chan struct{}
	// next 是本批每个界面调用之后的下一个界面调用的模型调用标识。
	next map[string]string
	// awaiting 表示本批已有界面调用挂起等待结果。
	awaiting bool
}

// newInterfaceSequencer 创建一次运行的界面调用排序。
func newInterfaceSequencer() *interfaceSequencer {
	return &interfaceSequencer{agents: map[string]*interfaceBatch{}}
}

// Name 返回扩展名称。
func (s *interfaceSequencer) Name() string { return interfaceSequencerName }

// batch 返回 Agent 当前一批的排序，调用方持有 mu。
func (s *interfaceSequencer) batch(agent string) *interfaceBatch {
	b, ok := s.agents[agent]
	if !ok {
		b = &interfaceBatch{turns: map[string]chan struct{}{}, next: map[string]string{}}
		s.agents[agent] = b
	}
	return b
}

// hold 记录 Agent 本批有界面调用挂起等待结果。
func (s *interfaceSequencer) hold(agent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch(agent).awaiting = true
}

// held 判断 Agent 本批是否已有界面调用挂起等待结果。
func (s *interfaceSequencer) held(agent string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batch(agent).awaiting
}

// AfterModelOutput 在工具执行前按模型输出登记 Agent 本批界面调用的先后顺序，第一个界面调用立即可以开始。
func (s *interfaceSequencer) AfterModelOutput(_ context.Context, scope einorun.AgentScope, output *schema.AgenticMessage) {
	batch := &interfaceBatch{turns: map[string]chan struct{}{}, next: map[string]string{}}
	previous := ""
	for _, block := range output.ContentBlocks {
		if block.Type != schema.ContentBlockTypeFunctionToolCall {
			continue
		}
		if spec, ok := lookupToolSpec(block.FunctionToolCall.Name); !ok || spec.computer == nil || !spec.computer.sequenced {
			continue
		}
		callID := block.FunctionToolCall.CallID
		batch.turns[callID] = make(chan struct{})
		if previous == "" {
			close(batch.turns[callID])
		} else {
			batch.next[previous] = callID
		}
		previous = callID
	}
	s.mu.Lock()
	s.agents[scope.ID] = batch
	s.mu.Unlock()
}

// ToolMiddlewares 让 Agent 的界面调用等到前一个界面调用结束再执行，结束后放行下一个；等待期间运行取消时不执行并放行下一个。
// 它位于工具执行的最外层，本批每个界面调用都经过它，后续调用才不会一直等待。
func (s *interfaceSequencer) ToolMiddlewares(scope einorun.AgentScope) []einorun.StagedToolMiddleware {
	return []einorun.StagedToolMiddleware{{Stage: einorun.StageOuter, Middleware: compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				s.mu.Lock()
				batch := s.batch(scope.ID)
				turn, ok := batch.turns[input.CallID]
				release := batch.turns[batch.next[input.CallID]]
				s.mu.Unlock()
				if !ok {
					return next(ctx, input)
				}
				// 本调用结束后放行下一个界面调用。
				if release != nil {
					defer close(release)
				}
				select {
				case <-turn:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return next(ctx, input)
			}
		},
	}}}
}
