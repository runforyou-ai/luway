package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/cloudwego/eino-ext/components/model/agenticark"
	"github.com/cloudwego/eino-ext/components/model/agenticclaude"
	"github.com/cloudwego/eino-ext/components/model/agenticdeepseek"
	"github.com/cloudwego/eino-ext/components/model/agenticgemini"
	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino-ext/components/model/agenticqwen"
	"github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"google.golang.org/genai"
)

// ChatConfig 定义一次上游对话请求使用的供应商入口、凭据、模型标识与请求参数。
type ChatConfig struct {
	Brand           string
	BaseURL         string
	APIKey          string
	Identifier      string
	MaxOutputTokens int
	DisableThinking bool              // 为 true 时在模型组件提供思考开关的品牌上关闭思考模式。
	Transport       http.RoundTripper // 非空时模型请求经该传输层发出。
}

// modelTransport 是模型请求的默认传输层，等待响应头最长 2 分钟，响应体读取时限由调用方 ctx 控制。
var modelTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 2 * time.Minute
	return transport
}()

// NewChatModel 按供应商品牌创建 eino-ext 的 Agentic 对话模型组件，没有品牌专用组件的供应商使用 OpenAI 兼容组件。
func NewChatModel(ctx context.Context, config ChatConfig) (model.AgenticModel, error) {
	baseURL, err := CompatibleBaseURL(config.Brand, config.BaseURL)
	if err != nil {
		return nil, err
	}
	var transport http.RoundTripper = modelTransport
	if config.Transport != nil {
		transport = config.Transport
	}
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var maxTokens *int
	if config.MaxOutputTokens > 0 {
		maxTokens = &config.MaxOutputTokens
	}
	var chatModel model.AgenticModel
	switch domain.AIProviderBrand(config.Brand) {
	case domain.AIProviderBrandDeepSeek:
		chatModel, err = agenticdeepseek.New(ctx, &agenticdeepseek.Config{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, MaxTokens: maxTokens, HTTPClient: httpClient,
		})
		// DeepSeek 组件没有思考开关，每次请求附加 thinking 参数关闭思考。
		if err == nil && config.DisableThinking {
			chatModel = &requestOptionsModel{AgenticModel: chatModel, options: []model.Option{
				openai.WithExtraFields(map[string]any{"thinking": map[string]any{"type": "disabled"}}),
			}}
		}
	case domain.AIProviderBrandAlibaba:
		componentConfig := &agenticqwen.Config{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, MaxTokens: maxTokens, HTTPClient: httpClient,
		}
		if config.DisableThinking {
			componentConfig.EnableThinking = new(false)
		}
		chatModel, err = agenticqwen.New(ctx, componentConfig)
	case domain.AIProviderBrandVolcengine:
		componentConfig := &agenticark.Config{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, MaxTokens: maxTokens, HTTPClient: httpClient,
		}
		if config.DisableThinking {
			componentConfig.Thinking = &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()}
		}
		chatModel, err = agenticark.New(ctx, componentConfig)
	case domain.AIProviderBrandAnthropic:
		// Anthropic 接口要求每次请求携带 max_tokens。
		if maxTokens == nil {
			return nil, errors.New("Anthropic model requires max output tokens")
		}
		chatModel, err = agenticclaude.New(ctx, &agenticclaude.Config{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, MaxTokens: config.MaxOutputTokens, HTTPClient: httpClient,
		})
	case domain.AIProviderBrandGoogle:
		var client *genai.Client
		client, err = genai.NewClient(ctx, &genai.ClientConfig{
			APIKey: config.APIKey, Backend: genai.BackendGeminiAPI, HTTPClient: httpClient,
			HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
		})
		if err != nil {
			break
		}
		chatModel, err = agenticgemini.New(ctx, &agenticgemini.Config{Client: client, Model: config.Identifier, MaxTokens: maxTokens})
	case domain.AIProviderBrandOpenAI:
		chatModel, err = agenticopenai.NewChatModel(ctx, &agenticopenai.ChatConfig{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, MaxCompletionTokens: maxTokens, HTTPClient: httpClient,
		})
	default:
		// 其余 OpenAI 兼容供应商通过请求体的 max_tokens 限制输出长度。
		componentConfig := &agenticopenai.ChatConfig{
			APIKey: config.APIKey, BaseURL: baseURL, Model: config.Identifier, HTTPClient: httpClient,
		}
		componentConfig.ExtraFields = map[string]any{}
		if maxTokens != nil {
			componentConfig.ExtraFields["max_tokens"] = *maxTokens
		}
		// 智谱通过请求体的 thinking 参数关闭思考，OpenRouter 通过 reasoning 参数关闭思考。
		switch {
		case !config.DisableThinking:
		case domain.AIProviderBrand(config.Brand) == domain.AIProviderBrandZhipu:
			componentConfig.ExtraFields["thinking"] = map[string]any{"type": "disabled"}
		case domain.AIProviderBrand(config.Brand) == domain.AIProviderBrandOpenRouter:
			componentConfig.ExtraFields["reasoning"] = map[string]any{"effort": "none"}
		}
		chatModel, err = agenticopenai.NewChatModel(ctx, componentConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("create %s agentic model: %w", config.Brand, err)
	}
	return chatModel, nil
}

// requestOptionsModel 在每次模型调用时附加固定的请求选项，调用方选项在后并可覆盖。
type requestOptionsModel struct {
	model.AgenticModel
	options []model.Option
}

// Generate 附加固定请求选项后调用模型。
func (m *requestOptionsModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return m.AgenticModel.Generate(ctx, input, append(slices.Clone(m.options), opts...)...)
}

// Stream 附加固定请求选项后流式调用模型。
func (m *requestOptionsModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return m.AgenticModel.Stream(ctx, input, append(slices.Clone(m.options), opts...)...)
}
