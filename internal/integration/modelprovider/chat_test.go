package modelprovider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

// optionCountingModel 记录每次调用收到的选项数量。
type optionCountingModel struct {
	optionCounts []int
}

// Generate 记录选项数量并返回空回复。
func (m *optionCountingModel) Generate(_ context.Context, _ []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.optionCounts = append(m.optionCounts, len(opts))
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant}, nil
}

// Stream 记录选项数量并返回单分片流。
func (m *optionCountingModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// TestRequestOptionsModelAppendsFixedOptions 验证固定请求选项随每次调用附加且不累积。
func TestRequestOptionsModelAppendsFixedOptions(t *testing.T) {
	inner := &optionCountingModel{}
	wrapped := &requestOptionsModel{AgenticModel: inner, options: []model.Option{model.WithTemperature(0)}}
	for range 2 {
		if _, err := wrapped.Generate(context.Background(), nil, model.WithMaxTokens(10)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := wrapped.Stream(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(inner.optionCounts) != 3 || inner.optionCounts[0] != 2 || inner.optionCounts[1] != 2 || inner.optionCounts[2] != 1 {
		t.Fatalf("option counts = %v", inner.optionCounts)
	}
}

// bodyRecordingTransport 记录模型组件发出的请求地址与请求体并返回请求错误。
type bodyRecordingTransport struct {
	urls   []string
	bodies []string
}

// RoundTrip 记录请求地址与请求体并返回 400 错误响应。
func (t *bodyRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.urls = append(t.urls, request.URL.String())
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		t.bodies = append(t.bodies, string(body))
	}
	return &http.Response{
		StatusCode: http.StatusBadRequest, Request: request,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"error":{"message":"rejected"}}`)),
	}, nil
}

// TestChatModelEndpoints 验证各品牌模型组件的同步与流式请求都落在品牌对话接口上。
func TestChatModelEndpoints(t *testing.T) {
	const base = "https://upstream.example.com/api"
	for brand, endpoint := range map[domain.AIProviderBrand]map[string]string{
		domain.AIProviderBrandOpenAI:     {"gpt-test": "/chat/completions"},
		domain.AIProviderBrandDeepSeek:   {"deepseek-test": "/chat/completions"},
		domain.AIProviderBrandAlibaba:    {"qwen-test": "/compatible-mode/v1/chat/completions"},
		domain.AIProviderBrandZhipu:      {"glm-test": "/chat/completions"},
		domain.AIProviderBrandOpenRouter: {"deepseek/deepseek-test": "/chat/completions"},
		domain.AIProviderBrandOllama:     {"llama-test": "/v1/chat/completions"},
		domain.AIProviderBrandVolcengine: {"doubao-test": "/responses"},
		domain.AIProviderBrandAnthropic:  {"claude-test": "/v1/messages"},
		domain.AIProviderBrandGoogle: {
			"gemini-test":        "/v1beta/models/gemini-test:",
			"models/gemini-test": "/v1beta/models/gemini-test:",
		},
	} {
		for identifier, path := range endpoint {
			transport := &bodyRecordingTransport{}
			chatModel, err := NewChatModel(context.Background(), ChatConfig{
				Brand: string(brand), APIKey: "placeholder", BaseURL: base, Identifier: identifier, MaxOutputTokens: 100, Transport: transport,
			})
			if err != nil {
				t.Fatalf("%s 创建模型失败：%v", brand, err)
			}
			input := []*schema.AgenticMessage{schema.UserAgenticMessage("你好")}
			_, _ = chatModel.Generate(context.Background(), input)
			if stream, err := chatModel.Stream(context.Background(), input); err == nil {
				for {
					if _, err := stream.Recv(); err != nil {
						break
					}
				}
			}
			if len(transport.urls) == 0 {
				t.Fatalf("%s %s 没有经传输层发出请求", brand, identifier)
			}
			for _, requested := range transport.urls {
				if !strings.HasPrefix(requested, base+path) {
					t.Fatalf("%s %s 请求地址 = %s", brand, identifier, requested)
				}
			}
		}
	}
}

// TestOpenRouterDisableThinking 验证 OpenRouter 关闭思考时请求体携带 reasoning.effort 为 none。
func TestOpenRouterDisableThinking(t *testing.T) {
	transport := &bodyRecordingTransport{}
	chatModel, err := NewChatModel(context.Background(), ChatConfig{
		Brand: string(domain.AIProviderBrandOpenRouter), APIKey: "placeholder", BaseURL: "https://openrouter.ai/api/v1",
		Identifier: "deepseek/deepseek-test", MaxOutputTokens: 100, DisableThinking: true, Transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = chatModel.Generate(context.Background(), []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	if len(transport.bodies) == 0 || !strings.Contains(transport.bodies[0], `"reasoning":{"effort":"none"}`) {
		t.Fatalf("bodies = %v", transport.bodies)
	}
}
