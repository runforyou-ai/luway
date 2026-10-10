//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	schemaclaude "github.com/cloudwego/eino/schema/claude"
	"github.com/runforyou-ai/einorun/llm"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	"github.com/runforyou-ai/einorun/provider/vendor"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// structuredReply 是结构化输出测试要求的对象。
type structuredReply struct {
	Answer string `json:"answer"`
	Score  *int   `json:"score" jsonschema:"nullable"`
}

// structuredScriptModel 按调用顺序返回预设输出，并记录每次请求的结构化输出参数。
type structuredScriptModel struct {
	mu      sync.Mutex
	outputs []*schema.AgenticMessage
	configs *[]modelprovider.ChatConfig
	config  modelprovider.ChatConfig
}

// Generate 返回下一条预设输出。
func (m *structuredScriptModel) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.configs = append(*m.configs, m.config)
	if len(m.outputs) == 0 {
		return nil, errors.New("script exhausted")
	}
	output := m.outputs[0]
	m.outputs = m.outputs[1:]
	return output, nil
}

// Stream 以单个分片返回下一条预设输出。
func (m *structuredScriptModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// TestCallStructuredRetriesThroughModelCalls 验证结构化单次调用把输出结构交给上游请求，正文无法解析时带纠正要求重试一次，
// 两次请求都经统一调用入口各记一条调用并累计用量；模型输出被截断时直接失败且不重试。
func TestCallStructuredRetriesThroughModelCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, _, modelID := newAIWorkspace(t)
	resolved, err := aimodel.Resolve(ctx, db, identity.Workspace.ID, modelID, domain.AIModelUsageAgent)
	require.NoError(t, err)
	scope := modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, identity.Workspace.ID)
	// caller 创建按 outputs 依次输出的调用入口，返回每次上游请求的参数记录。
	caller := func(outputs ...*schema.AgenticMessage) (agentruntime.ModelConfig, *[]modelprovider.ChatConfig) {
		configs := &[]modelprovider.ChatConfig{}
		script := &structuredScriptModel{outputs: outputs, configs: configs}
		upstreams := fakeUpstreams(nil, nil)
		upstreams.Chat = func(_ context.Context, config modelprovider.ChatConfig) (model.AgenticModel, error) {
			script.mu.Lock()
			script.config = config
			script.mu.Unlock()
			return script, nil
		}
		return modelcall.New(db, upstreams, nil).ModelConfig(scope, resolved), configs
	}

	before, _ := loadModelCalls(t, db, modelID)
	config, configs := caller(withUsage(assistantText("好的，答案如下"), 30, 4), withUsage(assistantText(`{"answer":"42","score":null}`), 40, 6))
	reply, usage, err := agentruntime.GenerateObject[structuredReply](ctx, config, "回答问题，输出 JSON", "问题")
	require.NoError(t, err)
	require.Equal(t, structuredReply{Answer: "42"}, reply)
	require.Equal(t, agentcontract.Usage{PromptTokens: 70, CompletionTokens: 10, TotalTokens: 80}, usage)
	require.Len(t, *configs, 2, "解析失败后重试一次")
	for _, requested := range *configs {
		require.NotNil(t, requested.Output, "上游请求携带输出结构")
		require.Equal(t, "structuredReply", requested.Output.Name)
		require.Equal(t, []string{"answer", "score"}, requested.Output.Schema.Required)
	}
	openAI, _ := vendor.Of(vendor.OpenAI)
	require.Equal(t, vendor.StructuredJSONSchema, openAI.Structured)
	calls, _ := loadModelCalls(t, db, modelID)
	calls = calls[len(before):]
	require.Len(t, calls, 2, "重试经统一调用入口记录")
	require.Equal(t, []int64{30, 40}, []int64{calls[0].InputTokens, calls[1].InputTokens})
	for _, call := range calls {
		require.Equal(t, string(domain.AIModelCallStatusSucceeded), call.Status)
	}

	truncated := withUsage(assistantText(`{"answer":"4`), 30, 4096)
	truncated.ResponseMeta.ClaudeExtension = &schemaclaude.ResponseMetaExtension{StopReason: "max_tokens"}
	config, configs = caller(truncated)
	_, _, err = agentruntime.GenerateObject[structuredReply](ctx, config, "回答问题，输出 JSON", "问题")
	require.ErrorIs(t, err, llm.ErrTruncated)
	require.Len(t, *configs, 1, "截断不重试")

	config, configs = caller(withUsage(assistantText("不是 JSON"), 30, 4), withUsage(assistantText("仍不是 JSON"), 30, 4))
	_, usage, err = agentruntime.GenerateObject[structuredReply](ctx, config, "回答问题，输出 JSON", "问题")
	require.Error(t, err)
	require.Len(t, *configs, 2, "只重试一次")
	require.Equal(t, 60, usage.PromptTokens)
}

// openAIUpstream 启动按请求体决定响应的 OpenAI Chat Completions 假上游，返回记录每次请求体的调用入口。
func openAIUpstream(t *testing.T, respond func(body map[string]any) (int, string)) (agentruntime.ModelConfig, *[]map[string]any, *bun.DB, string) {
	t.Helper()
	db, identity, _, modelID := newAIWorkspace(t)
	resolved, err := aimodel.Resolve(context.Background(), db, identity.Workspace.ID, modelID, domain.AIModelUsageAgent)
	require.NoError(t, err)
	var mu sync.Mutex
	requests := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		if !assert.NoError(t, err) {
			return
		}
		body := map[string]any{}
		assert.NoError(t, json.Unmarshal(raw, &body))
		mu.Lock()
		*requests = append(*requests, body)
		mu.Unlock()
		status, response := respond(body)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(ctx context.Context, config modelprovider.ChatConfig) (model.AgenticModel, error) {
		config.Brand, config.BaseURL = vendor.OpenAI, server.URL+"/v1"
		return modelprovider.NewChatModel(ctx, config)
	}
	scope := modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, identity.Workspace.ID)
	return modelcall.New(db, upstreams, nil).ModelConfig(scope, resolved), requests, db, modelID
}

// chatCompletion 返回只含一条助手消息的 Chat Completions 响应体。
func chatCompletion(message string) string {
	return `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-test","choices":[{"index":0,"finish_reason":"stop","message":` + message +
		`}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`
}

// TestCallStructuredFallsBackWhenResponseFormatUnsupported 验证上游以 HTTP 400 拒绝 response_format 时，同一次模型调用改为不带约束的请求并解析正文中的 JSON 对象，
// 两次上游请求只记一条成功的调用。
func TestCallStructuredFallsBackWhenResponseFormatUnsupported(t *testing.T) {
	t.Parallel()
	config, requests, db, modelID := openAIUpstream(t, func(body map[string]any) (int, string) {
		if _, constrained := body["response_format"]; constrained {
			return http.StatusBadRequest, `{"error":{"message":"Invalid parameter: 'response_format' of type 'json_schema' is not supported with this model.","type":"invalid_request_error","param":"response_format","code":null}}`
		}
		return http.StatusOK, chatCompletion(`{"role":"assistant","content":"{\"answer\":\"42\",\"score\":7}"}`)
	})

	reply, usage, err := agentruntime.GenerateObject[structuredReply](context.Background(), config, "回答问题，输出 JSON", "问题")
	require.NoError(t, err)
	score := 7
	require.Equal(t, structuredReply{Answer: "42", Score: &score}, reply)
	require.Equal(t, 20, usage.PromptTokens)
	require.Len(t, *requests, 2, "约束被拒后不带约束重新请求一次")
	require.Contains(t, (*requests)[0], "response_format")
	require.NotContains(t, (*requests)[1], "response_format")
	calls, _ := loadModelCalls(t, db, modelID)
	require.Len(t, calls, 1, "改为不约束的请求属于同一次模型调用")
	require.Equal(t, string(domain.AIModelCallStatusSucceeded), calls[0].Status)
}

// TestCallStructuredReturnsRefusalFromChatCompletions 验证 Chat Completions 响应的 message.refusal 被识别为拒绝，结构化调用返回 ErrOutputRefused 且不重试。
func TestCallStructuredReturnsRefusalFromChatCompletions(t *testing.T) {
	t.Parallel()
	config, requests, _, _ := openAIUpstream(t, func(map[string]any) (int, string) {
		return http.StatusOK, chatCompletion(`{"role":"assistant","content":null,"refusal":"I can't help with that."}`)
	})

	_, _, err := agentruntime.GenerateObject[structuredReply](context.Background(), config, "回答问题，输出 JSON", "问题")
	require.ErrorIs(t, err, llm.ErrRefused)
	require.Len(t, *requests, 1, "拒绝不重试")
}
