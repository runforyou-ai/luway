//go:build server

package integrationtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestAgentRoleBehavior 验证角色基线与场景规则的拼接、空企业指令的完整读写执行，以及运行行为快照的写入与沿用。
func TestAgentRoleBehavior(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "行为测试", DisplayName: "维护人员", Email: uniqueEmail("owner"), Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	identity := installed.Identity
	provider, err := aiprovideraction.NewCreateAIProviderAction(db).Execute(ctx, identity, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{Identifier: "chat", Name: "对话模型", Type: domain.AIModelTypeChat, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 32000, MaxOutputTokens: 4096}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 空企业指令可以创建、读取详情并再次保存。
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "行为助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: provider.ID, ModelIdentifier: "chat", SystemInstruction: "   "}},
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, agent.ID)
	if err != nil || detail.Execution.Managed == nil || detail.Execution.Managed.SystemInstruction != "" {
		t.Fatalf("agent detail with empty instruction = %+v, error = %v", detail, err)
	}
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: provider.ID, ModelIdentifier: "chat", SystemInstruction: ""}},
	}); err != nil {
		t.Fatal(err)
	}

	tasks, scheduler := newKnowledgeAgentScheduler(t, db)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "你好",
	})
	if err != nil {
		t.Fatal(err)
	}
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
		return agentruntime.RunResult{Content: "回复", EndSeq: claimed.EndSeq}, nil
	}}
	execute := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil)
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)

	// 指令由客服基线与内部对话场景规则拼成，空企业指令不占位，没有绑定知识库、未启用联网搜索时工具说明只有网页读取。
	if captured.Assignment.Scene != agentruntime.SceneAgentChat {
		t.Fatalf("scene = %q", captured.Assignment.Scene)
	}
	baselinePrefix := "你是企业「行为测试」的 AI 员工「行为助手」，专业领域是客户服务。"
	if !strings.HasPrefix(captured.Assignment.Instruction, baselinePrefix) || !strings.Contains(captured.Assignment.Instruction, "\n\n本次是企业内部对话") ||
		strings.Contains(captured.Assignment.Instruction, "\n\n\n") || !strings.Contains(captured.Assignment.Instruction, "可用工具：\n- web_fetch：") ||
		strings.Contains(captured.Assignment.Instruction, "search_knowledge") || strings.Contains(captured.Assignment.Instruction, "- web_search：") {
		t.Fatalf("instruction = %q", captured.Assignment.Instruction)
	}
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		HandlesCustomers  bool     `json:"handlesCustomers"`
		Scene             string   `json:"scene"`
		RulesVersion      int      `json:"rulesVersion"`
		Instruction       string   `json:"instruction"`
		InstructionSHA256 string   `json:"instructionSha256"`
		Tools             []string `json:"tools"`
		MCPServers        []string `json:"mcpServers"`
		Grounding         string   `json:"grounding"`
		Model             struct {
			ProviderID    string `json:"providerId"`
			Identifier    string `json:"identifier"`
			ContextWindow int64  `json:"contextWindow"`
		} `json:"model"`
	}
	if err := json.Unmarshal(run.BehaviorSnapshot, &snapshot); err != nil {
		t.Fatalf("behavior snapshot = %s, error = %v", run.BehaviorSnapshot, err)
	}
	sum := sha256.Sum256([]byte(captured.Assignment.Instruction))
	if !snapshot.HandlesCustomers || snapshot.Scene != string(agentruntime.SceneAgentChat) || snapshot.RulesVersion != agentruntime.AssignmentRulesVersion ||
		snapshot.Instruction != captured.Assignment.Instruction || snapshot.InstructionSHA256 != hex.EncodeToString(sum[:]) ||
		snapshot.Model.ProviderID != provider.ID || snapshot.Model.Identifier != "chat" || snapshot.Model.ContextWindow != 32000 ||
		strings.Join(snapshot.Tools, ",") != "calculator,web_fetch,TaskCreate,TaskGet,TaskUpdate,TaskList,agent" || len(snapshot.MCPServers) != 0 || snapshot.Grounding != "" {
		t.Fatalf("behavior snapshot = %+v", snapshot)
	}

	// 已写入快照的运行沿用快照中的指令，不重新拼接。
	if _, err := directchataction.NewSendAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "再问一句",
	}); err != nil {
		t.Fatal(err)
	}
	queued := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(queued).Where("agr.conversation_id = ? AND agr.status = ?", first.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model(queued).Set("behavior_snapshot = ?::jsonb", `{"handlesCustomers":true,"scene":"agent_chat","rulesVersion":1,"instruction":"已固定的指令","instructionSha256":"","model":{},"tools":[]}`).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)
	if captured.Assignment.Instruction != "已固定的指令" || captured.Assignment.Scene != agentruntime.SceneAgentChat {
		t.Fatalf("request after snapshot reuse = scene %q, instruction %q", captured.Assignment.Scene, captured.Assignment.Instruction)
	}

	// 客服场景使用客服场景规则，不注册计算器，客户历史检索进入工具说明与快照。
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "行为验证渠道", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "你们的退货政策是什么",
	})
	if err != nil {
		t.Fatal(err)
	}
	runQueuedAgentRun(t, db, execute, inbound.Conversation.ID)
	if captured.Assignment.Scene != agentruntime.SceneCustomer || captured.Assignment.Grounding != agentruntime.GroundingStrict || !strings.HasPrefix(captured.Assignment.Instruction, baselinePrefix) ||
		!strings.Contains(captured.Assignment.Instruction, "\n\n本次是客户会话") || !strings.Contains(captured.Assignment.Instruction, "追问、转人工、结束服务与其他工具不在同一次输出中同时调用。") ||
		!strings.HasSuffix(captured.Assignment.Instruction, "不重复之前的回答，不再查询资料。") ||
		!strings.Contains(captured.Assignment.Instruction, "- ask_customer：") || !strings.Contains(captured.Assignment.Instruction, "- handoff_to_human：") ||
		!strings.Contains(captured.Assignment.Instruction, "- resolve_conversation：") ||
		!strings.Contains(captured.Assignment.Instruction, "- search_customer_history：") || strings.Contains(captured.Assignment.Instruction, "人工客服跟进") {
		t.Fatalf("customer instruction = scene %q, %q", captured.Assignment.Scene, captured.Assignment.Instruction)
	}
	customerRun := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(customerRun).Where("agr.conversation_id = ?", inbound.Conversation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(customerRun.BehaviorSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Scene != string(agentruntime.SceneCustomer) || !slices.Equal(snapshot.Tools, []string{"search_customer_history", "ask_customer", "handoff_to_human", "resolve_conversation"}) ||
		snapshot.Grounding != string(agentruntime.GroundingStrict) {
		t.Fatalf("customer behavior snapshot = %+v", snapshot)
	}
}
