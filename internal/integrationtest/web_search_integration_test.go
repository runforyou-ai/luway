//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
		if fieldError, ok := errors.AsType[*common.FieldError](err); !ok || fieldError.Fields[invalid.field] != invalid.code {
			t.Fatalf("config=%+v err=%v", invalid.config, err)
		}
	}

	providerID := seedSummaryModels(t, f.db, f.owner)
	var captured agentruntime.RunRequest
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		captured = request
		triggers, err := feed.Peek(ctx, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: "好的", EndSeq: claimed.EndSeq}, nil
	}}
	tasks := newTestTasks(f.db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	execute := agentrunaction.NewExecuteAction(f.db, tasks, runtime, testAttachmentReader(f.db), nil, nil)
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		DisplayName: "资料助手 " + uuid.NewV7().String()[:8],
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: "chat-model",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 与 AI 员工新开一个单聊并执行排队运行，返回本次运行的有效配置工具清单。
	chat := func() []string {
		t.Helper()
		sent, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, agentrunaction.NewScheduler(tasks)).Execute(ctx, f.owner, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "查一下最新的退款政策",
		})
		if err != nil {
			t.Fatal(err)
		}
		run := &servermodels.AgentRun{}
		if err := f.db.NewSelect().Model(run).Where("agr.conversation_id = ?", sent.Conversation.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		captured = agentruntime.RunRequest{}
		if err := execute.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		return captured.Assignment.Tools
	}

	// 未启用联网搜索时只提供网页读取。
	tools := chat()
	if slices.Contains(tools, agentruntime.WebSearchToolName) || !slices.Contains(tools, agentruntime.WebFetchToolName) ||
		captured.WebSearch != nil || captured.WebFetch == nil {
		t.Fatalf("tools without web search = %v", tools)
	}

	// 保存时去掉云端服务商的实例地址与首尾空白，内部对话随后获得联网搜索。
	saved, err := update.Execute(ctx, f.owner, &websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: " tvly-key ", BaseURL: "https://ignored.example.com"})
	if err != nil || *saved != (websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: "tvly-key"}) {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	loaded, err := websearchaction.LoadConfig(ctx, f.db, f.owner.Organization.ID)
	if err != nil || loaded == nil || *loaded != *saved {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	tools = chat()
	if !slices.Contains(tools, agentruntime.WebSearchToolName) || captured.WebSearch == nil || captured.WebFetch == nil {
		t.Fatalf("tools with web search = %v", tools)
	}

	// 自托管服务保存去掉末尾斜杠的实例地址，不保存 API Key。
	selfHosted, err := update.Execute(ctx, f.owner, &websearch.Config{Provider: domain.WebSearchProviderSearXNG, APIKey: "unused", BaseURL: "https://search.example.com/"})
	if err != nil || *selfHosted != (websearch.Config{Provider: domain.WebSearchProviderSearXNG, BaseURL: "https://search.example.com"}) {
		t.Fatalf("self hosted=%+v err=%v", selfHosted, err)
	}

	// 关闭联网搜索后删除设置。
	if _, err := update.Execute(ctx, f.owner, nil); err != nil {
		t.Fatal(err)
	}
	if loaded, err := websearchaction.LoadConfig(ctx, f.db, f.owner.Organization.ID); err != nil || loaded != nil {
		t.Fatalf("loaded after disable=%+v err=%v", loaded, err)
	}
}
