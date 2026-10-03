package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/subagent"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	// subagentToolName 是把子任务委派给子 Agent 的工具名称。
	subagentToolName = "agent"
	// subagentName 是通用子 Agent 的类型名称。
	subagentName = "general"
)

const subagentDescription = "通用子 Agent：使用与你相同的工具（不含 agent 与任务清单工具），适合调研资料、查阅大量文件、独立完成一段修改等可以单独交付结果的任务。"

// subagentFactory 为每次委派创建独立的子 Agent，并行委派之间不共享中间件状态；子 Agent 使用有效配置中的子 Agent 指令，与主 Agent 使用同一模型和同一组工具，不含委派与任务清单工具。
type subagentFactory struct {
	runtime       *EinoRuntime
	request       RunRequest
	tools         []tool.BaseTool // 主 Agent 的普通工具，本机工具由每个子 Agent 各自创建。
	mediaEnabled  *atomic.Bool
	maxIterations int
	recorder      *processRecorder
	usage         sharedUsage
}

// sharedUsage 在并行执行的子 Agent 之间累计用量。
type sharedUsage struct {
	mu    sync.Mutex
	usage Usage
}

// merge 累计一个子 Agent 的用量。
func (u *sharedUsage) merge(other Usage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.usage.merge(other)
}

// total 返回已累计的用量。
func (u *sharedUsage) total() Usage {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.usage
}

// newSubagentMiddleware 创建委派子任务的中间件。
func newSubagentMiddleware(ctx context.Context, factory *subagentFactory) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	middleware, err := subagent.NewTyped(ctx, &subagent.TypedConfig[*schema.AgenticMessage]{
		SubAgents: []adk.TypedAgent[*schema.AgenticMessage]{factory},
		ToolName:  subagentToolName,
	})
	if err != nil {
		return nil, fmt.Errorf("create subagent middleware: %w", err)
	}
	return middleware, nil
}

// Name 返回子 Agent 的类型名称。
func (f *subagentFactory) Name(context.Context) string { return subagentName }

// Description 返回子 Agent 的适用范围。
func (f *subagentFactory) Description(context.Context) string { return subagentDescription }

// Get 为 fork 模式的技能提供通用子 Agent。
func (f *subagentFactory) Get(context.Context, string, *skill.TypedAgentHubOptions[*schema.AgenticMessage]) (adk.TypedAgent[*schema.AgenticMessage], error) {
	return f, nil
}

// Run 创建子 Agent 执行一次委派，事件原样转发，结束后把用量计入本次运行；子 Agent 调用工具时在委派调用上报告当前活动。
func (f *subagentFactory) Run(ctx context.Context, input *adk.TypedAgentInput[*schema.AgenticMessage], options ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.AgenticMessage]] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.AgenticMessage]]()
	delegation, _ := ctx.Value(toolCallContextKey{}).(toolCallMetadata)
	agent, usage, err := f.build(ctx, input, delegatedActivity{recorder: f.recorder, callID: delegation.CallID})
	if err != nil {
		generator.Send(&adk.TypedAgentEvent[*schema.AgenticMessage]{Err: err})
		generator.Close()
		return iterator
	}
	events := agent.Run(ctx, input, options...)
	go func() {
		defer generator.Close()
		for {
			event, ok := events.Next()
			if !ok {
				break
			}
			generator.Send(event)
		}
		f.usage.merge(usage())
	}()
	return iterator
}

// build 按主 Agent 的有效配置创建一个子 Agent，返回的函数在子 Agent 结束后给出其累计用量。
func (f *subagentFactory) build(ctx context.Context, input *adk.TypedAgentInput[*schema.AgenticMessage], observer toolObserver) (adk.TypedAgent[*schema.AgenticMessage], func() Usage, error) {
	workspace, err := newWorkspaceTools(ctx, f.request, f.mediaEnabled, nil)
	if err != nil {
		return nil, nil, err
	}
	counter := &usageCounter{}
	assembly, err := f.runtime.buildAgent(ctx, agentSpec{
		name: subagentName, description: subagentDescription, instruction: f.request.Assignment.DelegateInstruction,
		request: f.request, maxIterations: f.maxIterations, mediaEnabled: f.mediaEnabled,
		workspace: workspace, tools: append(slices.Clone(f.tools), workspace.tools...),
		toolMiddlewares: []compose.ToolMiddleware{toolExecutionMiddleware(observer)},
		observer:        counter, guard: newFinalIterationGuard(f.maxIterations, false),
	})
	if err != nil {
		return nil, nil, err
	}
	// 摘要保留委派任务本身。
	if len(input.Messages) > 0 {
		assembly.summarizer.keepFrom(input.Messages[len(input.Messages)-1])
	}
	return assembly.agent, func() Usage {
		total := counter.usage
		total.merge(assembly.auxiliaryUsage())
		return total
	}, nil
}

// usageCounter 在子 Agent 每次模型调用定稿后累计其用量，一个子 Agent 内的模型调用串行进行。
type usageCounter struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	usage Usage
}

// AfterModelRewriteState 累计本次模型输出的用量。
func (c *usageCounter) AfterModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if len(state.Messages) > 0 {
		c.usage.add(state.Messages[len(state.Messages)-1].ResponseMeta)
	}
	return ctx, state, nil
}

// delegatedActivity 把子 Agent 的工具调用报告为委派调用上的当前活动。
type delegatedActivity struct {
	recorder *processRecorder
	callID   string
}

// toolStarted 登记子 Agent 开始的工具调用，并把它记为委派调用的当前活动。
func (a delegatedActivity) toolStarted(ctx context.Context, input *compose.ToolInput, at time.Time) error {
	return a.recorder.childStarted(ctx, a.callID, input, at)
}

// toolFinished 记录子 Agent 工具调用的结果，委派调用的活动在下一次工具调用开始时更新；子 Agent 不挂起，等待外部结果按失败处理。
func (a delegatedActivity) toolFinished(ctx context.Context, input *compose.ToolInput, at time.Time, result string, err error) error {
	if errors.Is(err, ErrAwaitExternal) {
		err = errors.New("tool calls awaiting external results are unavailable to delegated agents")
	}
	return a.recorder.childFinished(ctx, a.callID, input, at, result, err)
}
