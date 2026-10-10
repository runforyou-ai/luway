//go:build server

package modelcall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/provider"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/agentruntime"
)

// errStreamClosed 表示调用方在流式输出结束前关闭了读取端。
var errStreamClosed = fmt.Errorf("model stream closed before completion: %w", context.Canceled)

// ModelConfig 返回运行时使用的模型参数，模型组件的每次请求都经统一入口按 scope 记录。
func (i *Invoker) ModelConfig(scope Scope, target *aimodel.Model) agentruntime.ModelConfig {
	return agentruntime.ModelConfig{
		MaxOutputTokens: int(target.MaxOutputTokens),
		ContextWindow:   int(target.ContextWindow),
		InputModalities: target.InputModalities,
		New:             i.ChatModels(scope, target),
	}
}

// ChatModels 返回对话模型组件工厂，组件的每次请求都经统一入口按 scope 记录并按本次调用的来源尝试顺序请求。
func (i *Invoker) ChatModels(scope Scope, target *aimodel.Model) llm.ModelFactory {
	return func(_ context.Context, options llm.ModelOptions) (model.AgenticModel, error) {
		return &chatModel{invoker: i, scope: scope, target: target, options: options, components: map[string]model.AgenticModel{}}, nil
	}
}

// chatModel 是经统一入口调用的对话模型组件，按来源缓存上游模型组件。
type chatModel struct {
	invoker *Invoker
	scope   Scope
	target  *aimodel.Model
	options llm.ModelOptions

	mu         sync.Mutex
	components map[string]model.AgenticModel
}

// component 返回来源对应的上游模型组件，首次使用时创建。
func (m *chatModel) component(ctx context.Context, route aimodel.Route) (model.AgenticModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if component, ok := m.components[route.ID]; ok {
		return component, nil
	}
	// 适配器写给模型的说明使用中文。
	component, err := m.invoker.upstreams.Chat(ctx, provider.ChatConfig{
		Brand: VendorBrand(route.Brand), BaseURL: route.APIURL, APIKey: route.APIKey, Model: route.Identifier,
		MaxOutputTokens: m.options.MaxOutputTokens, DisableThinking: m.options.DisableThinking, Output: m.options.Output,
		Language: llm.Chinese,
	})
	if err != nil {
		return nil, err
	}
	m.components[route.ID] = component
	return component, nil
}

// Generate 记录一次调用并按本次调用的来源尝试顺序请求完整输出。
func (m *chatModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	var message *schema.AgenticMessage
	err := m.invoker.run(ctx, m.scope, m.target, m.estimate(input), func(ctx context.Context, route aimodel.Route) (Usage, error) {
		component, err := m.component(ctx, route)
		if err != nil {
			return Usage{}, err
		}
		message, err = component.Generate(ctx, input, opts...)
		if err != nil {
			return Usage{}, err
		}
		return messageUsage(message.ResponseMeta), nil
	})
	return message, err
}

// Stream 记录一次调用并按本次调用的来源尝试顺序请求流式输出：来源在首个分片前失败时尝试下一来源，收到首个分片后固定该来源，流结束时写入结果与用量。
func (m *chatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	record, err := m.invoker.begin(ctx, m.scope, m.target, m.estimate(input))
	if err != nil {
		return nil, err
	}
	// 上游未报告输入时按上下文估算计费，工具定义无法估算时只计消息。
	billedInput, countErr := einorun.CountTokens(ctx, input, model.GetCommonOptions(&model.Options{}, opts...).Tools)
	if countErr != nil {
		billedInput, _ = einorun.CountTokens(ctx, input, nil)
	}
	err = errors.New("AI model has no route")
	for _, route := range record.routes {
		attemptID, beginErr := record.beginAttempt(ctx, route)
		if beginErr != nil {
			err = beginErr
			break
		}
		// 每次流式请求使用独立的可取消 context，调用方关闭读取端时取消上游请求。
		streamCtx, cancel := context.WithCancel(ctx)
		var upstream *schema.StreamReader[*schema.AgenticMessage]
		var first *schema.AgenticMessage
		upstream, first, err = m.open(streamCtx, route, input, opts)
		if err == nil {
			return relay(ctx, record, attemptID, upstream, first, cancel, billedInput), nil
		}
		cancel()
		record.finishAttempt(ctx, attemptID, Usage{}, err)
		if ctx.Err() != nil {
			break
		}
		slog.WarnContext(ctx, "模型来源请求失败", "ai_model_call_id", record.id, "route_id", route.ID, "provider_id", route.ProviderID, "error", err)
	}
	record.finish(ctx, err)
	return nil, err
}

// estimate 返回一次请求的预估用量：输入按消息编码长度预估，输出取本次请求的最大输出 Token 数。
func (m *chatModel) estimate(input []*schema.AgenticMessage) Usage {
	output := int64(m.options.MaxOutputTokens)
	if output <= 0 {
		output = m.target.MaxOutputTokens
	}
	return Usage{InputTokens: estimateValueTokens(input, m.target.ContextWindow), OutputTokens: output}
}

// open 请求来源的流式输出并读取首个分片；上游未输出任何分片即结束时返回空流与空分片。
func (m *chatModel) open(ctx context.Context, route aimodel.Route, input []*schema.AgenticMessage, opts []model.Option) (*schema.StreamReader[*schema.AgenticMessage], *schema.AgenticMessage, error) {
	component, err := m.component(ctx, route)
	if err != nil {
		return nil, nil, err
	}
	upstream, err := component.Stream(ctx, input, opts...)
	if err != nil {
		return nil, nil, err
	}
	first, err := upstream.Recv()
	if errors.Is(err, io.EOF) {
		upstream.Close()
		return nil, nil, nil
	}
	if err != nil {
		upstream.Close()
		return nil, nil, err
	}
	return upstream, first, nil
}

// relay 把首个分片与后续上游分片按调用方读取逐个转交，流结束、出错或调用方关闭读取端时取消上游请求，并按 deliveredUsage 写入上游尝试与调用的结果和用量；billedInput 是上游未报告输入时计费的输入估算。
func relay(ctx context.Context, record *call, attemptID string, upstream *schema.StreamReader[*schema.AgenticMessage], first *schema.AgenticMessage, cancel context.CancelFunc, billedInput int64) *schema.StreamReader[*schema.AgenticMessage] {
	if upstream == nil {
		cancel()
		record.finishAttempt(ctx, attemptID, Usage{}, nil)
		record.finish(ctx, nil)
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{})
	}
	// 调用方可在读取进行中关闭读取端，分片用量的记录与结算由 mu 串行。
	var mu sync.Mutex
	metas := make([]*schema.AgenticResponseMeta, 0, 2)
	var output outputMeter
	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			cancel()
			upstream.Close()
			mu.Lock()
			usage := deliveredUsage(streamUsage(metas), billedInput, output.bytes, err == nil)
			mu.Unlock()
			record.finishAttempt(ctx, attemptID, usage, err)
			record.finish(ctx, err)
		})
	}
	pending, done := first, false
	return pullStream(func() (*schema.AgenticMessage, error) {
		if done {
			return nil, io.EOF
		}
		chunk := pending
		pending = nil
		if chunk == nil {
			var err error
			chunk, err = upstream.Recv()
			if err != nil {
				done = true
				if errors.Is(err, io.EOF) {
					finish(nil)
				} else {
					finish(err)
				}
				return nil, err
			}
		}
		mu.Lock()
		metas = append(metas, chunk.ResponseMeta)
		output.add(chunk)
		mu.Unlock()
		return chunk, nil
	}, func() { finish(errStreamClosed) })
}

// deliveredUsage 返回已开始输出的流式请求的计费用量：上游未报告输入时按 billedInput 计；流完整结束时输出取上游报告值，未报告时按已转交内容预估；
// 流中途结束时上游可能只报告了部分输出，输出取上游报告值与已转交内容预估值中的较大者。
func deliveredUsage(reported Usage, billedInput int64, outputBytes int, completed bool) Usage {
	usage := reported
	if usage.InputTokens == 0 {
		usage.InputTokens = billedInput
	}
	if delivered := estimateTokens(outputBytes, 0); !completed || usage.OutputTokens == 0 {
		usage.OutputTokens = max(usage.OutputTokens, delivered)
	}
	return usage
}

// outputMeter 累计流式分片中模型生成内容的字节数：推理（含供应商扩展中的推理正文）、正文与工具调用的参数，工具名称按调用编号只计一次。
type outputMeter struct {
	bytes int
	named map[string]bool
}

// add 累计一个分片中模型生成内容的字节数。
func (m *outputMeter) add(chunk *schema.AgenticMessage) {
	for _, block := range chunk.ContentBlocks {
		switch {
		case block == nil:
		case block.Reasoning != nil:
			m.bytes += len(block.Reasoning.Text)
			if block.Reasoning.OpenAIExtension != nil {
				for _, content := range block.Reasoning.OpenAIExtension.Content {
					if content != nil {
						m.bytes += len(content.Text)
					}
				}
			}
		case block.AssistantGenText != nil:
			m.bytes += len(block.AssistantGenText.Text)
		case block.FunctionToolCall != nil:
			call := block.FunctionToolCall
			m.bytes += len(call.Arguments)
			// 部分供应商在工具调用的开始与结束分片中重复携带名称，同一调用编号的名称只计一次。
			if call.CallID == "" || !m.named[call.CallID] {
				m.bytes += len(call.Name)
			}
			if call.CallID != "" && call.Name != "" {
				if m.named == nil {
					m.named = map[string]bool{}
				}
				m.named[call.CallID] = true
			}
		}
	}
}

// messageUsage 读取一次完整输出的 Token 用量，输出未携带用量时为零。
func messageUsage(meta *schema.AgenticResponseMeta) Usage {
	if meta == nil || meta.TokenUsage == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:       int64(meta.TokenUsage.PromptTokens),
		CachedInputTokens: int64(meta.TokenUsage.PromptTokenDetails.CachedTokens),
		OutputTokens:      int64(meta.TokenUsage.CompletionTokens),
	}
}

// streamUsage 合并流式分片携带的 Token 用量，各项取分片中的最大值，与 eino 拼接流式输出的规则一致。
func streamUsage(metas []*schema.AgenticResponseMeta) Usage {
	var usage Usage
	for _, meta := range metas {
		chunk := messageUsage(meta)
		usage.InputTokens = max(usage.InputTokens, chunk.InputTokens)
		usage.CachedInputTokens = max(usage.CachedInputTokens, chunk.CachedInputTokens)
		usage.OutputTokens = max(usage.OutputTokens, chunk.OutputTokens)
	}
	return usage
}

// pullStream 返回按需拉取分片的流：调用方每次读取时在其协程中调用 recv，recv 以 io.EOF 表示结束；调用方关闭读取端后立即调用一次 onClose。
func pullStream[T any](recv func() (T, error), onClose func()) *schema.StreamReader[T] {
	ticks, feeder := schema.Pipe[struct{}](0)
	// 发送端阻塞到调用方取走一次读取机会或关闭读取端，关闭后执行收尾。
	go func() {
		for !feeder.Send(struct{}{}, nil) {
		}
		onClose()
	}()
	return schema.StreamReaderWithConvert(ticks, func(struct{}) (T, error) { return recv() })
}
