//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestWebSearchSettingsAndAgentTools 验证联网搜索设置的校验、保存与关闭，以及内部对话运行按设置获得联网搜索与网页读取。
func TestWebSearchSettingsAndAgentTools(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	update := websearchaction.NewUpdateSettingsAction(f.db)

	// 云端服务商缺少 API Key、自托管服务缺少或填错实例地址时逐字段报错。
	for _, invalid := range []struct {
		config websearch.Config
		field  string
		code   common.FieldCode
	}{
		{websearch.Config{Provider: "unknown", APIKey: "key"}, "provider", websearchaction.ValidationProviderInvalid},
		{websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: "  "}, "apiKey", websearchaction.ValidationAPIKeyRequired},
		{websearch.Config{Provider: domain.WebSearchProviderSearXNG}, "baseUrl", websearchaction.ValidationBaseURLRequired},
		{websearch.Config{Provider: domain.WebSearchProviderSearXNG, BaseURL: "ftp://search.example.com"}, "baseUrl", websearchaction.ValidationBaseURLInvalid},
	} {
		config := invalid.config
		_, err := update.Execute(ctx, f.owner, &config)
		var fieldError *common.FieldError
		require.ErrorAs(t, err, &fieldError, "config=%+v", invalid.config)
		require.Equal(t, invalid.code, fieldError.Fields[invalid.field], "config=%+v", invalid.config)
	}

	providerID := seedSummaryModels(t, f.db, f.owner)
	var captured agentruntime.RunRequest
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		captured = request
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: "好的", EndSeq: claimed.EndSeq}, nil
	}}
	tasks := servertest.NewTasks()
	execute := newTestAgentRun(f.db, tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		DisplayName: "资料助手 " + servertest.UniqueSuffix(),
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: aiModelID(t, f.db, providerID, "chat-model"),
		}},
	})
	require.NoError(t, err)
	// 与 AI 员工新开一个单聊并执行排队运行，返回本次运行的有效配置工具清单。
	chat := func() []string {
		t.Helper()
		sent, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, f.owner, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "查一下最新的退款政策",
		})
		require.NoError(t, err)
		run := &servermodels.AgentRun{}
		require.NoError(t, f.db.NewSelect().Model(run).Where("agr.conversation_id = ?", sent.Conversation.ID).Scan(ctx))
		captured = agentruntime.RunRequest{}
		require.NoError(t, execute.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
		return captured.Assignment.Tools
	}

	// 未启用联网搜索时只提供网页读取。
	tools := chat()
	require.NotContains(t, tools, agentruntime.WebSearchToolName)
	require.Contains(t, tools, agentruntime.WebFetchToolName)
	require.Nil(t, captured.WebSearch)
	require.NotNil(t, captured.WebFetch)

	// 保存时去掉云端服务商的实例地址与首尾空白，内部对话随后获得联网搜索。
	saved, err := update.Execute(ctx, f.owner, &websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: " tvly-key ", BaseURL: "https://ignored.example.com"})
	require.NoError(t, err)
	require.Equal(t, websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: "tvly-key"}, *saved)
	loaded, err := websearchaction.LoadConfig(ctx, f.db, f.owner.Workspace.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, *saved, *loaded)
	tools = chat()
	require.Contains(t, tools, agentruntime.WebSearchToolName)
	require.NotNil(t, captured.WebSearch)
	require.NotNil(t, captured.WebFetch)

	// 自托管服务保存去掉末尾斜杠的实例地址，不保存 API Key。
	selfHosted, err := update.Execute(ctx, f.owner, &websearch.Config{Provider: domain.WebSearchProviderSearXNG, APIKey: "unused", BaseURL: "https://search.example.com/"})
	require.NoError(t, err)
	require.Equal(t, websearch.Config{Provider: domain.WebSearchProviderSearXNG, BaseURL: "https://search.example.com"}, *selfHosted)

	// 关闭联网搜索后删除设置。
	_, err = update.Execute(ctx, f.owner, nil)
	require.NoError(t, err)
	loaded, err = websearchaction.LoadConfig(ctx, f.db, f.owner.Workspace.ID)
	require.NoError(t, err)
	require.Nil(t, loaded, "loaded after disable")
}
