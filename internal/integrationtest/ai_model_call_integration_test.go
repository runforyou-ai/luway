//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openaiext "github.com/cloudwego/eino/schema/openai"
	"github.com/runforyou-ai/einorun/llm"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	"github.com/runforyou-ai/einorun/provider/embedding"
	"github.com/runforyou-ai/einorun/provider/rerank"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
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
	if m.identifier == "upstream-timeout-model" {
		return nil, fmt.Errorf("upstream %s: %w", m.identifier, context.DeadlineExceeded)
	}
	message := assistantText("来自 " + m.identifier)
	message.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{
		PromptTokens: 10, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 4}, CompletionTokens: 5, TotalTokens: 15,
	}}
	return message, nil
}

// Stream 以三个分片返回回复，末尾分片携带用量；按标识在首个分片前失败。unreported-model 不报告用量，依次输出供应商扩展中的推理、正文，以及开始与结束分片重复携带名称的工具调用；early-usage-model 在首个分片报告输入与少量输出。
func (m *upstreamChat) Stream(ctx context.Context, _ []*schema.AgenticMessage, _ ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m.failing[m.identifier] {
		return nil, errors.New("upstream " + m.identifier + " unavailable")
	}
	switch m.identifier {
	case "unreported-model":
		reasoning := &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.Reasoning{
			OpenAIExtension: &openaiext.ReasoningExtension{Content: []*openaiext.ReasoningContent{{Text: "思考中"}}},
		})}}
		toolCall := func(arguments string) *schema.AgenticMessage {
			return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.FunctionToolCall{CallID: "call-1", Name: "lookup", Arguments: arguments})}}
		}
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{reasoning, assistantText("答复"), toolCall(""), toolCall(`{"q":1}`)}), nil
	case "early-usage-model":
		first := assistantText("来自")
		first.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 50, CompletionTokens: 1, TotalTokens: 51}}
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{first, assistantText("下文")}), nil
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
			return &upstreamChat{identifier: config.Model, failing: failing, blocking: blocking}, nil
		},
		Embedder: embedderFunc(func(_ context.Context, _ embedding.Endpoint, _ string, dimension int, inputs []string) (embedding.Result, error) {
			vectors := make([][]float32, len(inputs))
			for index := range vectors {
				vectors[index] = make([]float32, dimension)
			}
			return embedding.Result{Vectors: vectors, InputTokens: 7 * len(inputs)}, nil
		}),
		Reranker: rerankerFunc(func(_ context.Context, _ rerank.Endpoint, _, _ string, documents []string, _ int) (rerank.Result, error) {
			return rerank.Result{Scores: []rerank.Score{{Index: 0, Relevance: 0.9}}, InputTokens: 30}, nil
		}),
		Decider: deciderFunc(func(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error) {
			return map[string]decision.Answer{"ok": {Kind: decision.KindYesNo, Probability: 0.8}}, nil
		}),
	}
}

// embedderFunc 把函数适配为向量客户端。
type embedderFunc func(context.Context, embedding.Endpoint, string, int, []string) (embedding.Result, error)

// Embed 调用函数。
func (f embedderFunc) Embed(ctx context.Context, credential embedding.Endpoint, model string, dimension int, inputs []string) (embedding.Result, error) {
	return f(ctx, credential, model, dimension, inputs)
}

// rerankerFunc 把函数适配为重排客户端。
type rerankerFunc func(context.Context, rerank.Endpoint, string, string, []string, int) (rerank.Result, error)

// Rerank 调用函数。
func (f rerankerFunc) Rerank(ctx context.Context, credential rerank.Endpoint, model, query string, documents []string, topN int) (rerank.Result, error) {
	return f(ctx, credential, model, query, documents, topN)
}

// deciderFunc 把函数适配为判断客户端。
type deciderFunc func(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error)

// Decide 调用函数。
func (f deciderFunc) Decide(ctx context.Context, credential decision.Credential, model string, state any, questions map[string]decision.Question) (map[string]decision.Answer, error) {
	return f(ctx, credential, model, state, questions)
}

// addModelRoute 为模型添加一个权重为 0 的备用来源：新建同工作区供应商，并以指定上游标识和优先级写入路由。
func addModelRoute(t *testing.T, db bun.IDB, workspaceID, modelID, identifier string, priority int, enabled bool) string {
	t.Helper()
	ctx := context.Background()
	provider := &servermodels.AIProvider{
		WorkspaceID: &workspaceID, Brand: string(domain.AIProviderBrandOpenRouter), Name: "备用来源 " + identifier,
		CredentialType: string(domain.AIProviderCredentialTypeAPIKey), APIKey: "backup-key", APIURL: "https://backup.example.com/v1",
	}
	_, err := db.NewInsert().Model(provider).Column("workspace_id", "brand", "name", "credential_type", "api_key", "api_url").Returning("id").Exec(ctx)
	require.NoError(t, err)
	route := &servermodels.AIModelRoute{ModelID: modelID, ProviderID: provider.ID, Identifier: identifier, Priority: priority, Weight: 0, Enabled: enabled}
	_, err = db.NewInsert().Model(route).Column("model_id", "provider_id", "identifier", "priority", "weight", "enabled").Exec(ctx)
	require.NoError(t, err)
	return provider.ID
}

// loadModelCalls 按开始顺序读取模型的调用记录及其上游尝试。
func loadModelCalls(t *testing.T, db bun.IDB, modelID string) ([]servermodels.AIModelCall, map[string][]servermodels.AIModelCallAttempt) {
	t.Helper()
	ctx := context.Background()
	calls := make([]servermodels.AIModelCall, 0)
	require.NoError(t, db.NewSelect().Model(&calls).Where("amc.model_id = ?", modelID).Order("amc.id ASC").Scan(ctx))
	attempts := make(map[string][]servermodels.AIModelCallAttempt, len(calls))
	for _, call := range calls {
		rows := make([]servermodels.AIModelCallAttempt, 0)
		require.NoError(t, db.NewSelect().Model(&rows).Where("amca.call_id = ?", call.ID).Order("amca.id ASC").Scan(ctx))
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
	workspaceID := identity.Workspace.ID
	addModelRoute(t, db, workspaceID, modelID, "backup-model", 1, true)
	addModelRoute(t, db, workspaceID, modelID, "disabled-model", 2, false)

	resolved, err := aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
	require.NoError(t, err)
	require.Len(t, resolved.Routes, 2)
	require.Equal(t, "chat-model", resolved.Routes[0].Identifier)
	require.Equal(t, "backup-model", resolved.Routes[1].Identifier)
	invoker := modelcall.New(db, fakeUpstreams(map[string]bool{"chat-model": true}, nil), nil)
	scope := modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, identity.Workspace.ID)
	chatModel, err := invoker.ChatModels(scope, resolved)(ctx, llm.ModelOptions{})
	require.NoError(t, err)

	// 首选来源失败后切换到备用来源，调用成功并记录备用来源的用量。
	message, err := chatModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.NoError(t, err)
	require.Equal(t, "来自 backup-model", message.ContentBlocks[0].AssistantGenText.Text)
	calls, attempts := loadModelCalls(t, db, modelID)
	call := calls[0]
	require.Len(t, calls, 1)
	require.Equal(t, string(domain.AIModelCallStatusSucceeded), call.Status)
	require.Equal(t, workspaceID, call.WorkspaceID)
	require.Equal(t, string(domain.AIModelUsageAgent), call.ModelUsage)
	require.Equal(t, "测试对话模型", call.ModelName)
	require.Equal(t, string(domain.AIModelCallActorMember), call.ActorType)
	require.NotNil(t, call.ActorID)
	require.Equal(t, identity.WorkspaceIdentity.ID, *call.ActorID)
	require.Equal(t, string(domain.AIModelCallSourceConversation), call.SourceType)
	require.Equal(t, int64(10), call.InputTokens)
	require.Equal(t, int64(4), call.CachedInputTokens)
	require.Equal(t, int64(5), call.OutputTokens)
	require.NotNil(t, call.FinishedAt)
	tried := attempts[call.ID]
	require.Len(t, tried, 2)
	require.Equal(t, "chat-model", tried[0].Identifier)
	require.Equal(t, string(domain.AIModelCallStatusFailed), tried[0].Status)
	require.NotEmpty(t, tried[0].ErrorMessage)
	require.Equal(t, "backup-model", tried[1].Identifier)
	require.Equal(t, string(domain.AIModelCallStatusSucceeded), tried[1].Status)
	require.Equal(t, int64(5), tried[1].OutputTokens)

	// 流式输出：首选来源在首个分片前失败时切换，流结束后写入合并的用量。
	stream, err := chatModel.Stream(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.NoError(t, err)
	chunks := 0
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		chunks++
	}
	stream.Close()
	streamed := waitModelCallFinished(t, db, modelID)
	require.Equal(t, 3, chunks)
	require.Equal(t, string(domain.AIModelCallStatusSucceeded), streamed.Status)
	require.Equal(t, int64(20), streamed.InputTokens)
	require.Equal(t, int64(3), streamed.OutputTokens)

	// 调用方读到首个分片后关闭流，调用记为已取消；上游未报告用量，输入按预估计，输出按已转交的「来自」6 字节计 2 个 Token。
	stream, err = chatModel.Stream(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	stream.Close()
	closed := waitModelCallFinished(t, db, modelID)
	require.Equal(t, string(domain.AIModelCallStatusCanceled), closed.Status, "closed call=%+v", closed)
	require.Equal(t, int64(2), closed.InputTokens)
	require.Equal(t, int64(2), closed.OutputTokens)

	// streamFrom 只启用指定上游标识的来源，读取 reads 个分片后关闭流（reads 为负时读到结束），返回结束后的调用记录。
	streamFrom := func(identifier string, reads int) servermodels.AIModelCall {
		t.Helper()
		addModelRoute(t, db, workspaceID, modelID, identifier, 10, true)
		routed, err := aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
		require.NoError(t, err)
		routed.Routes = arr.Filter(routed.Routes, func(route aimodel.Route) bool { return route.Identifier == identifier })
		single, err := invoker.ChatModels(scope, routed)(ctx, llm.ModelOptions{})
		require.NoError(t, err)
		stream, err := single.Stream(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
		require.NoError(t, err)
		for read := 0; reads < 0 || read < reads; read++ {
			if _, err := stream.Recv(); errors.Is(err, io.EOF) {
				break
			}
		}
		stream.Close()
		return waitModelCallFinished(t, db, modelID)
	}
	// 完整结束但上游不报告用量：输入按上下文估算计「你好」2 个 Token；输出计扩展推理「思考中」、正文「答复」、只计一次的工具名称与参数共 28 字节，即 7 个 Token。
	unreported := streamFrom("unreported-model", -1)
	require.Equal(t, string(domain.AIModelCallStatusSucceeded), unreported.Status)
	require.Equal(t, [2]int64{2, 7}, [2]int64{unreported.InputTokens, unreported.OutputTokens})
	// 上游在首个分片报告了输入与少量输出后停止：输入取上游报告值，输出取报告值与已转交「来自」2 个 Token 中的较大者。
	early := streamFrom("early-usage-model", 1)
	require.Equal(t, string(domain.AIModelCallStatusCanceled), early.Status)
	require.Equal(t, [2]int64{50, 2}, [2]int64{early.InputTokens, early.OutputTokens})

	// 全部来源失败时返回最后一次尝试的错误，调用记为失败；前面的失败已使主来源熔断，尝试顺序由熔断决定。
	failing := modelcall.New(db, fakeUpstreams(map[string]bool{"chat-model": true, "backup-model": true}, nil), nil)
	failingModel, _ := failing.ChatModels(scope, resolved)(ctx, llm.ModelOptions{})
	_, err = failingModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.Error(t, err, "全部来源失败时调用成功")
	failed := waitModelCallFinished(t, db, modelID)
	_, attempts = loadModelCalls(t, db, modelID)
	failedAttempts := attempts[failed.ID]
	require.Len(t, failedAttempts, 2)
	require.Equal(t, string(domain.AIModelCallStatusFailed), failed.Status)
	require.Equal(t, failedAttempts[1].ErrorMessage, failed.ErrorMessage)

	// 调用超时记为超时，被调用方时限打断的尝试记为已取消，且不再尝试后续来源。
	blocking := modelcall.New(db, fakeUpstreams(nil, map[string]bool{"chat-model": true, "backup-model": true}), nil)
	blockingModel, _ := blocking.ChatModels(scope, resolved)(ctx, llm.ModelOptions{})
	timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = blockingModel.Generate(timeoutCtx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.ErrorIs(t, err, context.DeadlineExceeded, "timeout")
	timedOut := waitModelCallFinished(t, db, modelID)
	_, attempts = loadModelCalls(t, db, modelID)
	require.Equal(t, string(domain.AIModelCallStatusTimedOut), timedOut.Status)
	require.Len(t, attempts[timedOut.ID], 1)
	require.Equal(t, string(domain.AIModelCallStatusCanceled), attempts[timedOut.ID][0].Status)

	// 删除供应商时删除其模型及模型的全部来源。
	require.NoError(t, aiprovideraction.NewDeleteAIProviderAction(db).Execute(ctx, identity, providerID))
	remaining, err := db.NewSelect().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, remaining, "remaining routes")
	_, err = aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
	require.ErrorIs(t, err, aimodel.ErrUnavailable, "resolve deleted model")
}

// TestModelCallWeightedRoutesAndBreaker 验证来源按权重随机决定先尝试的来源、权重为 0 的来源只作备用，以及连续失败的来源熔断后排到最后、冷却结束后恢复。
func TestModelCallWeightedRoutesAndBreaker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, _, modelID := newAIWorkspace(t)
	workspaceID := identity.Workspace.ID
	addModelRoute(t, db, workspaceID, modelID, "backup-model", 1, true)
	scope := modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, workspaceID)
	setWeight := func(identifier string, weight int) {
		t.Helper()
		_, err := db.NewUpdate().Model((*servermodels.AIModelRoute)(nil)).Set("weight = ?", weight).
			Where("model_id = ? AND identifier = ?", modelID, identifier).Exec(ctx)
		require.NoError(t, err)
	}
	// generate 用给定上游执行 count 次调用，返回每次调用先尝试的上游标识及最后一次调用尝试的标识。
	generate := func(failing map[string]bool, count int) ([]string, []string) {
		t.Helper()
		resolved, err := aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
		require.NoError(t, err)
		chatModel, err := modelcall.New(db, fakeUpstreams(failing, nil), nil).ChatModels(scope, resolved)(ctx, llm.ModelOptions{})
		require.NoError(t, err)
		_, before := loadModelCalls(t, db, modelID)
		for range count {
			_, err := chatModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
			require.NoError(t, err)
		}
		calls, attempts := loadModelCalls(t, db, modelID)
		firsts := make([]string, 0, count)
		for _, call := range calls {
			if _, seen := before[call.ID]; !seen {
				firsts = append(firsts, attempts[call.ID][0].Identifier)
			}
		}
		last := arr.Map(attempts[calls[len(calls)-1].ID], func(attempt servermodels.AIModelCallAttempt) string { return attempt.Identifier })
		return firsts, last
	}

	// 权重为 0 的来源排在有权重的来源之后，不论列表顺序。
	setWeight("chat-model", 0)
	setWeight("backup-model", 5)
	firsts, _ := generate(nil, 5)
	require.Equal(t, []string{"backup-model", "backup-model", "backup-model", "backup-model", "backup-model"}, firsts)

	// 两个权重相同的来源都会被选为先尝试的来源。
	setWeight("chat-model", 1)
	setWeight("backup-model", 1)
	firsts, _ = generate(nil, 40)
	require.Contains(t, firsts, "chat-model")
	require.Contains(t, firsts, "backup-model")

	// 主来源连续 3 次失败后熔断，之后的调用直接使用备用来源。
	setWeight("backup-model", 0)
	failing := map[string]bool{"chat-model": true}
	firsts, _ = generate(failing, 3)
	require.Equal(t, []string{"chat-model", "chat-model", "chat-model"}, firsts)
	_, last := generate(failing, 1)
	require.Equal(t, []string{"backup-model"}, last)

	// 熔断中的来源在其他来源都失败时仍会尝试。
	resolved, err := aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
	require.NoError(t, err)
	allFailing, err := modelcall.New(db, fakeUpstreams(map[string]bool{"chat-model": true, "backup-model": true}, nil), nil).ChatModels(scope, resolved)(ctx, llm.ModelOptions{})
	require.NoError(t, err)
	_, err = allFailing.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.Error(t, err)
	calls, attempts := loadModelCalls(t, db, modelID)
	tried := arr.Map(attempts[calls[len(calls)-1].ID], func(attempt servermodels.AIModelCallAttempt) string { return attempt.Identifier })
	require.Equal(t, []string{"backup-model", "chat-model"}, tried)

	// 熔断按结束时间取最近的尝试：较早开始的成功尝试最后结束时不熔断。
	_, err = db.NewUpdate().TableExpr("ai_model_call_attempts").Set("finished_at = now() + interval '1 second'").
		Where("id = (SELECT a.id FROM ai_model_call_attempts AS a JOIN ai_model_routes AS amr ON amr.id = a.route_id WHERE amr.model_id = ? AND amr.identifier = 'chat-model' AND a.status = ? ORDER BY a.id LIMIT 1)", modelID, domain.AIModelCallStatusSucceeded).Exec(ctx)
	require.NoError(t, err)
	firsts, _ = generate(nil, 1)
	require.Equal(t, []string{"chat-model"}, firsts)
	// setChatAttempts 把主来源的全部尝试改为指定状态，并让它们在其他尝试之后结束。
	setChatAttempts := func(status domain.AIModelCallStatus) {
		t.Helper()
		_, err := db.NewUpdate().TableExpr("ai_model_call_attempts").Set("status = ?, finished_at = now() + interval '2 seconds'", status).
			Where("route_id IN (SELECT id FROM ai_model_routes WHERE model_id = ? AND identifier = 'chat-model')", modelID).Exec(ctx)
		require.NoError(t, err)
	}
	// 调用方取消或时限到期的尝试记为已取消，不计入熔断。
	setChatAttempts(domain.AIModelCallStatusCanceled)
	firsts, _ = generate(nil, 1)
	require.Equal(t, []string{"chat-model"}, firsts)
	// 上游自身超时计入熔断。
	setChatAttempts(domain.AIModelCallStatusTimedOut)
	firsts, _ = generate(nil, 1)
	require.Equal(t, []string{"backup-model"}, firsts)
	setChatAttempts(domain.AIModelCallStatusCanceled)

	// 重新连续失败 3 次使主来源熔断，熔断期间先尝试备用来源。
	firsts, _ = generate(failing, 3)
	require.Equal(t, []string{"chat-model", "chat-model", "chat-model"}, firsts)
	firsts, _ = generate(nil, 1)
	require.Equal(t, []string{"backup-model"}, firsts)
	// 最近一次失败超过冷却时长后熔断解除，主来源恢复为先尝试的来源。
	_, err = db.NewUpdate().TableExpr("ai_model_call_attempts").Set("finished_at = now() - interval '2 minutes'").
		Where("route_id IN (SELECT id FROM ai_model_routes WHERE model_id = ? AND identifier = 'chat-model')", modelID).Exec(ctx)
	require.NoError(t, err)
	firsts, _ = generate(failing, 1)
	require.Equal(t, []string{"chat-model"}, firsts)
	// 期满后再失败一次即重新熔断。
	firsts, _ = generate(nil, 1)
	require.Equal(t, []string{"backup-model"}, firsts)

	// 调用方仍有效时上游返回超时，尝试与调用都记为超时。
	addModelRoute(t, db, workspaceID, modelID, "upstream-timeout-model", 5, true)
	routed, err := aimodel.Resolve(ctx, db, workspaceID, modelID, domain.AIModelUsageAgent)
	require.NoError(t, err)
	routed.Routes = arr.Filter(routed.Routes, func(route aimodel.Route) bool { return route.Identifier == "upstream-timeout-model" })
	timeoutModel, err := modelcall.New(db, fakeUpstreams(nil, nil), nil).ChatModels(scope, routed)(ctx, llm.ModelOptions{})
	require.NoError(t, err)
	_, err = timeoutModel.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("你好")})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	calls, attempts = loadModelCalls(t, db, modelID)
	timedOut := calls[len(calls)-1]
	require.Equal(t, string(domain.AIModelCallStatusTimedOut), timedOut.Status)
	require.Equal(t, string(domain.AIModelCallStatusTimedOut), attempts[timedOut.ID][0].Status)
}
