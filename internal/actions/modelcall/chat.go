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
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
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

// ChatModels 返回对话模型组件工厂，组件的每次请求都经统一入口按 scope 记录并按来源顺序尝试。
func (i *Invoker) ChatModels(scope Scope, target *aimodel.Model) agentruntime.ModelFactory {
	return func(_ context.Context, options agentruntime.ModelOptions) (model.AgenticModel, error) {
		return &chatModel{invoker: i, scope: scope, target: target, options: options, components: map[string]model.AgenticModel{}}, nil
	}
}

// chatModel 是经统一入口调用的对话模型组件，按来源缓存上游模型组件。
type chatModel struct {
	invoker *Invoker
	scope   Scope
	target  *aimodel.Model
	options agentruntime.ModelOptions

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
	component, err := m.invoker.upstreams.Chat(ctx, modelprovider.ChatConfig{
		Brand: string(route.Brand), BaseURL: route.APIURL, APIKey: route.APIKey, Identifier: route.Identifier,
		MaxOutputTokens: m.options.MaxOutputTokens, DisableThinking: m.options.DisableThinking,
	})
	if err != nil {
		return nil, err
	}
	m.components[route.ID] = component
	return component, nil
}

// Generate 记录一次调用并按来源顺序请求完整输出。
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

// Stream 记录一次调用并按来源顺序请求流式输出：来源在首个分片前失败时尝试下一来源，收到首个分片后固定该来源，流结束时写入结果与用量。
func (m *chatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	record, err := m.invoker.begin(ctx, m.scope, m.target, m.estimate(input))
	if err != nil {
		return nil, err
	}
	err = errors.New("AI model has no route")
	for _, route := range m.target.Routes {
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
			return relay(ctx, record, attemptID, upstream, first, cancel), nil
		}
		cancel()
		record.finishAttempt(ctx, attemptID, Usage{}, err)
		if ctx.Err() != nil {
			break
		}
		slog.Warn("模型来源请求失败", "ai_model_call_id", record.id, "route_id", route.ID, "provider_id", route.ProviderID, "error", err)
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

// relay 把首个分片与后续上游分片按调用方读取逐个转交，流结束、出错或调用方关闭读取端时取消上游请求，并写入上游尝试与调用的结果和用量。
func relay(ctx context.Context, record *call, attemptID string, upstream *schema.StreamReader[*schema.AgenticMessage], first *schema.AgenticMessage, cancel context.CancelFunc) *schema.StreamReader[*schema.AgenticMessage] {
	if upstream == nil {
		cancel()
		record.finishAttempt(ctx, attemptID, Usage{}, nil)
		record.finish(ctx, nil)
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{})
	}
	// 调用方可在读取进行中关闭读取端，分片用量的记录与结算由 mu 串行。
	var mu sync.Mutex
	metas := make([]*schema.AgenticResponseMeta, 0, 2)
	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			cancel()
			upstream.Close()
			mu.Lock()
			usage := streamUsage(metas)
			mu.Unlock()
			record.finishAttempt(ctx, attemptID, usage, err)
			record.finish(ctx, err)
		})
	}
	pending, done := first, false
	return modelprovider.PullStream(func() (*schema.AgenticMessage, error) {
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
		mu.Unlock()
		return chunk, nil
	}, func() { finish(errStreamClosed) })
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
