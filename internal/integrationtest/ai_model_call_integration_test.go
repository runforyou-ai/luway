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
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/luway/internal/integration/modelgateway"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/rerank"
	"github.com/uptrace/bun"
)

// upstreamChat 是按上游模型标识决定行为的假对话模型组件：failing 中的标识在输出前失败，blocking 中的标识等到调用 context 结束。
type upstreamChat struct {
	identifier string
	failing    map[string]bool
	blocking   map[string]bool
	options    *[]model.Option
}

// Generate 返回带固定用量的回复或按标识失败。
func (m *upstreamChat) Generate(ctx context.Context, _ []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if m.options != nil {
		*m.options = opts
	}
	if m.blocking[m.identifier] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.failing[m.identifier] {
		return nil, errors.New("upstream " + m.identifier + " unavailable")
	}
	message := assistantText("来自 " + m.identifier)
	message.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{
		PromptTokens: 10, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 4}, CompletionTokens: 5, TotalTokens: 15,
	}}
	return message, nil
}

// Stream 以三个分片返回回复，末尾分片携带用量；按标识在首个分片前失败。
func (m *upstreamChat) Stream(ctx context.Context, _ []*schema.AgenticMessage, _ ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m.failing[m.identifier] {
		return nil, errors.New("upstream " + m.identifier + " unavailable")
	}
	last := assistantText("。")
	last.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23}}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{
		assistantText("来自"), assistantText(m.identifier), last,
	}), nil
}

// assistantText 构造只含一段正文的助手消息。
func assistantText(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: text})}}
}

// fakeUpstreams 返回按标识决定行为的上游客户端。
func fakeUpstreams(failing, blocking map[string]bool) modelcall.Upstreams {
	return modelcall.Upstreams{
		Chat: func(_ context.Context, config modelprovider.ChatConfig) (model.AgenticModel, error) {
			return &upstreamChat{identifier: config.Identifier, failing: failing, blocking: blocking}, nil
		},
		Embedder: embedderFunc(func(_ context.Context, _ embedding.Credential, _ string, dimension int, inputs []string) (embedding.Result, error) {
			vectors := make([][]float32, len(inputs))
			for index := range vectors {
				vectors[index] = make([]float32, dimension)
			}
			return embedding.Result{Vectors: vectors, InputTokens: 7 * len(inputs)}, nil
		}),
		Reranker: rerankerFunc(func(_ context.Context, _ rerank.Credential, _, _ string, documents []string, _ int) (rerank.Result, error) {
			return rerank.Result{Scores: []rerank.Score{{Index: 0, Relevance: 0.9}}, InputTokens: 30}, nil
		}),
		Decider: deciderFunc(func(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error) {
			return map[string]decision.Answer{"ok": {Kind: decision.KindYesNo, Probability: 0.8}}, nil
		}),
	}
}

// embedderFunc 把函数适配为向量客户端。
type embedderFunc func(context.Context, embedding.Credential, string, int, []string) (embedding.Result, error)

// Embed 调用函数。
func (f embedderFunc) Embed(ctx context.Context, credential embedding.Credential, model string, dimension int, inputs []string) (embedding.Result, error) {
	return f(ctx, credential, model, dimension, inputs)
}

// rerankerFunc 把函数适配为重排客户端。
type rerankerFunc func(context.Context, rerank.Credential, string, string, []string, int) (rerank.Result, error)

// Rerank 调用函数。
func (f rerankerFunc) Rerank(ctx context.Context, credential rerank.Credential, model, query string, documents []string, topN int) (rerank.Result, error) {
	return f(ctx, credential, model, query, documents, topN)
}

// deciderFunc 把函数适配为判断客户端。
type deciderFunc func(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error)

// Decide 调用函数。
func (f deciderFunc) Decide(ctx context.Context, credential decision.Credential, model string, state any, questions map[string]decision.Question) (map[string]decision.Answer, error) {
	return f(ctx, credential, model, state, questions)
}

// addModelRoute 为模型添加一个来源：新建同工作区供应商，并以指定上游标识和优先级写入路由。
func addModelRoute(t *testing.T, db bun.IDB, organizationID, modelID, identifier string, priority int, enabled bool) string {
	t.Helper()
	ctx := context.Background()
	provider := &servermodels.AIProvider{
		OrganizationID: organizationID, Brand: string(domain.AIProviderBrandOpenRouter), Name: "备用来源 " + identifier,
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "backup-key", APIURL: "https://backup.example.com/v1",
	}
	if _, err := db.NewInsert().Model(provider).Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	route := &servermodels.AIModelRoute{ModelID: modelID, ProviderID: provider.ID, Identifier: identifier, Priority: priority, Enabled: enabled}
	if _, err := db.NewInsert().Model(route).Column("model_id", "provider_id", "identifier", "priority", "enabled").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return provider.ID
}

// loadModelCalls 按开始顺序读取模型的调用记录及其上游尝试。
func loadModelCalls(t *testing.T, db bun.IDB, modelID string) ([]servermodels.AIModelCall, map[string][]servermodels.AIModelCallAttempt) {
	t.Helper()
	ctx := context.Background()
	calls := make([]servermodels.AIModelCall, 0)
	if err := db.NewSelect().Model(&calls).Where("amc.model_id = ?", modelID).Order("amc.id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	attempts := make(map[string][]servermodels.AIModelCallAttempt, len(calls))
	for _, call := range calls {
		rows := make([]servermodels.AIModelCallAttempt, 0)
		if err := db.NewSelect().Model(&rows).Where("amca.call_id = ?", call.ID).Order("amca.id ASC").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		attempts[call.ID] = rows
	}
	return calls, attempts
}

// waitModelCallFinished 等待模型最近一次调用写入结束状态。
func waitModelCallFinished(t *testing.T, db bun.IDB, modelID string) servermodels.AIModelCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		calls, _ := loadModelCalls(t, db, modelID)
		if len(calls) > 0 && calls[len(calls)-1].FinishedAt != nil {
			return calls[len(calls)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("调用未结束：%+v", calls)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestModelCallRoutesAndRecords 验证统一调用入口按优先级尝试已启用来源、失败时切换下一来源，并记录调用归属、状态、用量与每次上游尝试。
func TestModelCallRoutesAndRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, providerID, modelID := newAIWorkspace(t)
	organizationID := identity.Organization.ID
	addModelRoute(t, db, organizationID, modelID, "backup-model", 1, true)
	addModelRoute(t, db, organizationID, modelID, "disabled-model", 2, false)

	resolved, err := aimodel.Resolve(ctx, db, organizationID, modelID, domain.AIModelUsageAgent)
	if err != nil || len(resolved.Routes) != 2 || resolved.Routes[0].Identifier != "chat-model" || resolved.Routes[1].Identifier != "backup-model" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	invoker := modelcall.New(db, fakeUpstreams(map[string]bool{"chat-model": true}, nil))
	scope := modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, identity.Organization.ID)
	chatModel, err := invoker.ChatModels(scope, resolved)(ctx, agentruntime.ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 首选来源失败后切换到备用来源，调用成功并记录备用来源的用量。
	message, err := chatModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	if err != nil || message.ContentBlocks[0].AssistantGenText.Text != "来自 backup-model" {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	calls, attempts := loadModelCalls(t, db, modelID)
	call := calls[0]
	if len(calls) != 1 || call.Status != string(domain.AIModelCallStatusSucceeded) || call.OrganizationID != organizationID ||
		call.ModelUsage != string(domain.AIModelUsageAgent) || call.ModelName != "测试对话模型" ||
		call.ActorType != string(domain.AIModelCallActorMember) || call.ActorID == nil || *call.ActorID != identity.OrganizationIdentity.ID ||
		call.SourceType != string(domain.AIModelCallSourceConversation) ||
		call.InputTokens != 10 || call.CachedInputTokens != 4 || call.OutputTokens != 5 || call.FinishedAt == nil {
		t.Fatalf("call=%+v", call)
	}
	tried := attempts[call.ID]
	if len(tried) != 2 || tried[0].Identifier != "chat-model" || tried[0].Status != string(domain.AIModelCallStatusFailed) || tried[0].ErrorMessage == "" ||
		tried[1].Identifier != "backup-model" || tried[1].Status != string(domain.AIModelCallStatusSucceeded) || tried[1].OutputTokens != 5 {
		t.Fatalf("attempts=%+v", tried)
	}

	// 流式输出：首选来源在首个分片前失败时切换，流结束后写入合并的用量。
	stream, err := chatModel.Stream(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	if err != nil {
		t.Fatal(err)
	}
	chunks := 0
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		chunks++
	}
	stream.Close()
	streamed := waitModelCallFinished(t, db, modelID)
	if chunks != 3 || streamed.Status != string(domain.AIModelCallStatusSucceeded) || streamed.InputTokens != 20 || streamed.OutputTokens != 3 {
		t.Fatalf("chunks=%d call=%+v", chunks, streamed)
	}

	// 调用方读到首个分片后关闭流，调用记为已取消。
	stream, err = chatModel.Stream(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	stream.Close()
	closed := waitModelCallFinished(t, db, modelID)
	if closed.Status != string(domain.AIModelCallStatusCanceled) {
		t.Fatalf("closed call=%+v", closed)
	}

	// 全部来源失败时返回最后一次错误，调用记为失败。
	failing := modelcall.New(db, fakeUpstreams(map[string]bool{"chat-model": true, "backup-model": true}, nil))
	failingModel, _ := failing.ChatModels(scope, resolved)(ctx, agentruntime.ModelOptions{})
	if _, err := failingModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")}); err == nil {
		t.Fatal("全部来源失败时调用成功")
	}
	if failed := waitModelCallFinished(t, db, modelID); failed.Status != string(domain.AIModelCallStatusFailed) || failed.ErrorMessage != "upstream backup-model unavailable" {
		t.Fatalf("failed call=%+v", failed)
	}

	// 调用超时记为超时，且不再尝试后续来源。
	blocking := modelcall.New(db, fakeUpstreams(nil, map[string]bool{"chat-model": true}))
	blockingModel, _ := blocking.ChatModels(scope, resolved)(ctx, agentruntime.ModelOptions{})
	timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := blockingModel.Generate(timeoutCtx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}
	timedOut := waitModelCallFinished(t, db, modelID)
	calls, attempts = loadModelCalls(t, db, modelID)
	if timedOut.Status != string(domain.AIModelCallStatusTimedOut) || len(attempts[timedOut.ID]) != 1 {
		t.Fatalf("timed out call=%+v attempts=%+v", timedOut, attempts[timedOut.ID])
	}

	// 删除供应商时删除其模型及模型的全部来源。
	if err := aiprovideraction.NewDeleteAIProviderAction(db).Execute(ctx, identity, providerID); err != nil {
		t.Fatal(err)
	}
	if remaining, err := db.NewSelect().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID).Count(ctx); err != nil || remaining != 0 {
		t.Fatalf("remaining routes=%d err=%v", remaining, err)
	}
	if _, err := aimodel.Resolve(ctx, db, organizationID, modelID, domain.AIModelUsageAgent); !errors.Is(err, aimodel.ErrUnavailable) {
		t.Fatalf("resolve deleted model err=%v", err)
	}
}

// TestModelCallServices 验证向量化、重排与判断经统一调用入口执行并记录后台调用归属与用量。
func TestModelCallServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, providerID, _ := newAIWorkspace(t)
	organizationID := identity.Organization.ID
	embeddingModel := &testAIModel{ProviderID: providerID, Identifier: "embedding-model", Name: "向量模型", Type: string(domain.AIModelTypeEmbedding), InputModalities: json.RawMessage(`["text"]`), ContextWindow: 8192}
	rerankModel := &testAIModel{ProviderID: providerID, Identifier: "rerank-model", Name: "重排模型", Type: string(domain.AIModelTypeRerank), InputModalities: json.RawMessage(`["text"]`), ContextWindow: 8192}
	decisionModel := &testAIModel{ProviderID: providerID, Identifier: "decision-model", Name: "判断模型", Type: string(domain.AIModelTypeDecision), InputModalities: json.RawMessage(`["text"]`), ContextWindow: 8192}
	insertAIModels(t, db, embeddingModel, rerankModel, decisionModel)
	invoker := modelcall.New(db, fakeUpstreams(nil, nil))
	scope := modelcall.SystemScope(organizationID, domain.AIModelCallSourceServiceSession, identity.Organization.ID)

	resolved, err := aimodel.Resolve(ctx, db, organizationID, embeddingModel.ID, domain.AIModelUsageEmbedding)
	if err != nil {
		t.Fatal(err)
	}
	if vectors, err := invoker.Embed(ctx, scope, resolved, 4, []string{"甲", "乙"}); err != nil || len(vectors) != 2 || len(vectors[0]) != 4 {
		t.Fatalf("vectors=%v err=%v", vectors, err)
	}
	resolved, err = aimodel.Resolve(ctx, db, organizationID, rerankModel.ID, domain.AIModelUsageRerank)
	if err != nil {
		t.Fatal(err)
	}
	if scores, err := invoker.Rerank(ctx, scope, resolved, "查询", []string{"甲"}, 1); err != nil || len(scores) != 1 {
		t.Fatalf("scores=%v err=%v", scores, err)
	}
	resolved, err = aimodel.Resolve(ctx, db, organizationID, decisionModel.ID, domain.AIModelUsageDecision)
	if err != nil {
		t.Fatal(err)
	}
	if answers, err := invoker.Decide(ctx, scope, resolved, "状态", map[string]decision.Question{"ok": {Kind: decision.KindYesNo, Instructions: "是否成立"}}); err != nil || answers["ok"].Probability != 0.8 {
		t.Fatalf("answers=%v err=%v", answers, err)
	}
	for modelID, expected := range map[string]struct {
		usage  domain.AIModelUsage
		tokens int64
	}{
		embeddingModel.ID: {domain.AIModelUsageEmbedding, 14},
		rerankModel.ID:    {domain.AIModelUsageRerank, 30},
		decisionModel.ID:  {domain.AIModelUsageDecision, 0},
	} {
		calls, attempts := loadModelCalls(t, db, modelID)
		if len(calls) != 1 || calls[0].ModelUsage != string(expected.usage) || calls[0].InputTokens != expected.tokens ||
			calls[0].Status != string(domain.AIModelCallStatusSucceeded) || calls[0].ActorType != string(domain.AIModelCallActorSystem) || calls[0].ActorID != nil ||
			len(attempts[calls[0].ID]) != 1 {
			t.Fatalf("model %s calls=%+v attempts=%+v", modelID, calls, attempts)
		}
	}
}

// TestModelGatewayRoundTrip 验证设备侧模型组件经网关把消息、工具与创建参数传给服务端统一调用入口，并收到完整与流式输出。
func TestModelGatewayRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, _, modelID := newAIWorkspace(t)
	resolved, err := aimodel.Resolve(ctx, db, identity.Organization.ID, modelID, domain.AIModelUsageAgent)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var received []model.Option
	var created []modelprovider.ChatConfig
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(_ context.Context, config modelprovider.ChatConfig) (model.AgenticModel, error) {
		mu.Lock()
		created = append(created, config)
		mu.Unlock()
		return &upstreamChat{identifier: config.Identifier, options: &received}, nil
	}
	invoker := modelcall.New(db, upstreams)
	scope := modelcall.AgentScope(identity.Organization.ID, identity.OrganizationIdentity.ID, domain.AIModelCallSourceAgentRun, modelID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		decoded, err := modelgateway.DecodeRequest(request.Body)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if err := modelgateway.Serve(request.Context(), writer, invoker.ChatModels(scope, resolved), decoded); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
		}
	}))
	defer server.Close()

	chatModel, err := modelgateway.Models(server.URL, http.DefaultTransport)(ctx, agentruntime.ModelOptions{MaxOutputTokens: 256, DisableThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	tool := &schema.ToolInfo{Name: "lookup", Desc: "查询", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"query": {Type: schema.String, Desc: "查询内容", Required: true},
	})}
	input := schema.UserAgenticMessage("你好")
	input.Extra = map[string]any{"signature": []byte{1, 2, 3}}
	message, err := chatModel.Generate(ctx, []*schema.AgenticMessage{input}, model.WithTools([]*schema.ToolInfo{tool}), model.WithTemperature(0.2))
	if err != nil || message.ContentBlocks[0].AssistantGenText.Text != "来自 chat-model" {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	mu.Lock()
	options := model.GetCommonOptions(nil, received...)
	config := created[0]
	mu.Unlock()
	if len(options.Tools) != 1 || options.Tools[0].Name != "lookup" || options.Tools[0].ParamsOneOf == nil || options.Temperature == nil || *options.Temperature != 0.2 {
		t.Fatalf("options=%+v", options)
	}
	if parameters, err := options.Tools[0].ToJSONSchema(); err != nil || parameters.Properties.Len() != 1 {
		t.Fatalf("parameters=%+v err=%v", parameters, err)
	}
	if config.MaxOutputTokens != 256 || !config.DisableThinking || config.Identifier != "chat-model" || config.APIKey != "test-key" {
		t.Fatalf("config=%+v", config)
	}

	stream, err := chatModel.Stream(ctx, []*schema.AgenticMessage{input})
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]*schema.AgenticMessage, 0, 3)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	concatenated, err := schema.ConcatAgenticMessages(chunks)
	if err != nil || len(chunks) != 3 || concatenated.ResponseMeta.TokenUsage.PromptTokens != 20 {
		t.Fatalf("chunks=%d message=%+v err=%v", len(chunks), concatenated, err)
	}
	streamed := waitModelCallFinished(t, db, modelID)
	calls, _ := loadModelCalls(t, db, modelID)
	if len(calls) != 2 || streamed.Status != string(domain.AIModelCallStatusSucceeded) || streamed.ActorType != string(domain.AIModelCallActorAgent) ||
		streamed.SourceType != string(domain.AIModelCallSourceAgentRun) {
		t.Fatalf("calls=%+v", calls)
	}
}
