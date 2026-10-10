//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// usageScriptedModel 是按调用顺序依次输出脚本步骤的对话模型，末尾分片携带该步骤的用量；primary 为 true 的组件在首个分片前失败。
type usageScriptedModel struct {
	primary bool
	script  *usageScript
}

// usageScript 是多个来源组件共享的脚本进度。
type usageScript struct {
	mu    sync.Mutex
	steps []*schema.AgenticMessage
	next  int
}

// Generate 返回下一个脚本步骤。
func (m *usageScriptedModel) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	if m.primary {
		return nil, errors.New("primary route unavailable")
	}
	m.script.mu.Lock()
	defer m.script.mu.Unlock()
	if m.script.next >= len(m.script.steps) {
		return nil, errors.New("script exhausted")
	}
	step := m.script.steps[m.script.next]
	m.script.next++
	return step, nil
}

// Stream 以正文分片加用量分片返回下一个脚本步骤。
func (m *usageScriptedModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	step, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	content := &schema.AgenticMessage{Role: step.Role, ContentBlocks: step.ContentBlocks}
	meta := &schema.AgenticMessage{Role: step.Role, ResponseMeta: step.ResponseMeta}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{content, meta}), nil
}

// withUsage 为模型输出附上用量。
func withUsage(message *schema.AgenticMessage, prompt, completion int) *schema.AgenticMessage {
	message.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{
		PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
	}}
	return message
}

// TestAgentRunUsageProjectsModelCalls 验证托管运行的用量等于该运行全部对话模型调用记录的汇总：
// 首选来源失败的上游尝试、调用工具的输出、因空正文被重试丢弃的输出与最终回答都按调用记录计入。
func TestAgentRunUsageProjectsModelCalls(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	addModelRoute(t, db, identity.Workspace.ID, modelID, "backup-model", 1, true)
	toolCall := assistantText("")
	toolCall.ContentBlocks = append(toolCall.ContentBlocks, schema.NewContentBlock(&schema.FunctionToolCall{CallID: "call-" + uuid.NewV7().String(), Name: "TaskList", Arguments: `{}`}))
	script := &usageScript{steps: []*schema.AgenticMessage{
		withUsage(toolCall, 120, 15),
		withUsage(assistantText(""), 140, 2),
		withUsage(assistantText("任务清单为空"), 141, 9),
	}}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(_ context.Context, config modelprovider.ChatConfig) (model.AgenticModel, error) {
		return &usageScriptedModel{primary: config.Model == "chat-model", script: script}, nil
	}
	tasks := servertest.NewTasks()
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	runner := newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "用量助手 " + servertest.UniqueSuffix(),
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	conversationID := uuid.NewV7().String()
	_, err = directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "看看任务清单",
	})
	require.NoError(t, err)
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
	require.NoError(t, runner.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
	require.Equal(t, len(script.steps), script.next, "脚本步骤未全部执行")

	calls := make([]servermodels.AIModelCall, 0)
	require.NoError(t, db.NewSelect().Model(&calls).
		Where("amc.source_type = ? AND amc.source_id = ?", domain.AIModelCallSourceAgentRun, run.ID).
		Order("amc.id ASC").Scan(ctx))
	require.Len(t, calls, len(script.steps), "每次模型输出一条调用记录")
	var aggregate agentcontract.Usage
	for _, call := range calls {
		require.Equal(t, string(domain.AIModelUsageAgent), call.ModelUsage)
		require.Equal(t, string(domain.AIModelCallStatusSucceeded), call.Status)
		aggregate.PromptTokens += int(call.InputTokens)
		aggregate.CompletionTokens += int(call.OutputTokens)
		aggregate.TotalTokens += int(call.InputTokens + call.OutputTokens)
		attempts := make([]servermodels.AIModelCallAttempt, 0)
		require.NoError(t, db.NewSelect().Model(&attempts).Where("amca.call_id = ?", call.ID).Order("amca.id ASC").Scan(ctx))
		require.Len(t, attempts, 2, "首选来源失败后切换备用来源")
		require.Equal(t, string(domain.AIModelCallStatusFailed), attempts[0].Status)
		require.Zero(t, attempts[0].InputTokens+attempts[0].OutputTokens, "失败的上游尝试没有用量")
	}
	require.Equal(t, agentcontract.Usage{PromptTokens: 401, CompletionTokens: 26, TotalTokens: 427}, aggregate)

	var raw string
	require.NoError(t, db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("usage").Where("id = ?", run.ID).Scan(ctx, &raw))
	var projected agentcontract.Usage
	require.NoError(t, json.Unmarshal([]byte(raw), &projected))
	require.Equal(t, aggregate, projected, "运行用量等于调用记录汇总")
}
