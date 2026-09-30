//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// testCustomerReplyGenerator 记录回复候选生成请求并返回预设结果。
type testCustomerReplyGenerator struct {
	requests []agentruntime.ReplyCandidatesRequest
	result   agentruntime.ReplyCandidatesResult
	err      error
}

// GenerateReplyCandidates 记录请求并返回预设结果。
func (g *testCustomerReplyGenerator) GenerateReplyCandidates(_ context.Context, request agentruntime.ReplyCandidatesRequest) (agentruntime.ReplyCandidatesResult, error) {
	g.requests = append(g.requests, request)
	return g.result, g.err
}

// TestServiceReplySuggestions 验证 AI 写回复的资格校验、上下文范围，以及不产生运行记录和消息。
func TestServiceReplySuggestions(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "回复建议助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "你是售后客服"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "AI 写回复", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), nil)
	visitor := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "上一轮的问题"}
	earlier, err := receive.Execute(ctx, visitor)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := earlier.Conversation.ID
	closeSession := servicesessionaction.NewCloseServiceSessionAction(db, agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), nil, nil), newTestTasks(db))
	if _, err := closeSession.Execute(ctx, identity, conversationID); err != nil {
		t.Fatal(err)
	}
	visitor.ConversationID, visitor.ClientMessageID, visitor.Body = &conversationID, uuid.NewV7().String(), "订单还没发货"
	current, err := receive.Execute(ctx, visitor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我来帮您查询",
	}); err != nil {
		t.Fatal(err)
	}
	// 生成上下文只包含对客消息。
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "内部备注：这是重点客户",
		Visibility: domain.MessageVisibilityInternal,
	}); err != nil {
		t.Fatal(err)
	}
	messageCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}

	generator := &testCustomerReplyGenerator{result: agentruntime.ReplyCandidatesResult{Candidates: []string{"您好，已为您加急处理。", "马上帮您催促仓库发货。"}}}
	action := agentrunaction.NewGenerateServiceReplySuggestionsAction(db, generator, testAttachmentReader(db))
	valid := agentrunaction.ServiceReplySuggestionsInput{
		ConversationID: conversationID, AgentIdentityID: created.IdentityID,
		Mode: domain.ServiceReplyModeRewrite, Tone: domain.ServiceReplyToneFriendly,
		Draft: "  帮您催一下  ", ReplyToMessageID: current.Message.ID,
	}
	candidates, err := action.Execute(ctx, identity, valid)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(candidates, generator.result.Candidates) || len(generator.requests) != 1 {
		t.Fatalf("candidates = %#v, requests = %d", candidates, len(generator.requests))
	}
	request := generator.requests[0]
	if len(request.History) != 2 || request.History[0].Role != agentruntime.MessageRoleUser || request.History[0].Content != "订单还没发货" ||
		request.History[1].Role != agentruntime.MessageRoleAssistant || request.History[1].Content != "我来帮您查询" {
		t.Fatalf("history = %#v", request.History)
	}
	if request.Model.Identifier != modelID || !strings.HasPrefix(request.Instruction, "你是售后客服") || !strings.Contains(request.Instruction, `{"candidates":`) {
		t.Fatalf("request configuration = %#v", request)
	}
	for _, expected := range []string{"改写客服草稿", "友好", `回复针对的引用消息：{"sender":"customer","content":"订单还没发货"}`, `客服草稿："帮您催一下"`} {
		if !strings.Contains(request.Task, expected) {
			t.Fatalf("task does not contain %q: %s", expected, request.Task)
		}
	}
	runCount, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if runCount != 0 || afterCount != messageCount {
		t.Fatalf("agent runs = %d, messages = %d, want 0 and %d", runCount, afterCount, messageCount)
	}

	// 写回复模式忽略草稿。
	reply := valid
	reply.Mode, reply.Draft, reply.ReplyToMessageID = domain.ServiceReplyModeReply, "", ""
	if _, err := action.Execute(ctx, identity, reply); err != nil {
		t.Fatal(err)
	}
	if task := generator.requests[len(generator.requests)-1].Task; strings.Contains(task, "客服草稿") || strings.Contains(task, "引用消息") {
		t.Fatalf("reply mode task = %s", task)
	}

	t.Run("输入校验", func(t *testing.T) {
		input := valid
		input.Draft, input.Mode, input.Tone = " ", domain.ServiceReplyModeRewrite, "loud"
		_, err := action.Execute(ctx, identity, input)
		validationError, ok := errors.AsType[*conversationaction.ValidationError](err)
		if !ok || validationError.Fields["draft"] != conversationaction.ValidationBodyRequired || validationError.Fields["tone"] != agentrunaction.ValidationServiceReplyToneInvalid {
			t.Fatalf("validation error = %v", err)
		}
	})
	t.Run("引用消息不可用", func(t *testing.T) {
		input := valid
		input.ReplyToMessageID = uuid.NewV7().String()
		_, err := action.Execute(ctx, identity, input)
		if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflictError.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
			t.Fatalf("reply target error = %v", err)
		}
	})
	t.Run("会话不存在", func(t *testing.T) {
		input := valid
		input.ConversationID = uuid.NewV7().String()
		if _, err := action.Execute(ctx, identity, input); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("conversation error = %v", err)
		}
	})
	t.Run("AI 员工停用", func(t *testing.T) {
		setAgentStatus := func(status domain.IdentityStatus) {
			if _, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", status).Where("identity_id = ?", created.IdentityID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		setAgentStatus(domain.IdentityStatusInactive)
		defer setAgentStatus(domain.IdentityStatusActive)
		if _, err := action.Execute(ctx, identity, valid); !errors.Is(err, agentrunaction.ErrAgentUnavailable) {
			t.Fatalf("agent error = %v", err)
		}
	})
	t.Run("其他负责人", func(t *testing.T) {
		if _, err := db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).Set("assignee_identity_id = ?", created.IdentityID).Where("id = ?", current.Conversation.ServiceSessionID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).Set("assignee_identity_id = ?", identity.OrganizationIdentity.ID).Where("id = ?", current.Conversation.ServiceSessionID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}()
		_, err := action.Execute(ctx, identity, valid)
		if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflictError.Reason != conversationaction.ConflictReasonServiceSessionOwned {
			t.Fatalf("owned error = %v", err)
		}
	})
	t.Run("生成失败", func(t *testing.T) {
		generator.err = errors.New("model unavailable")
		defer func() { generator.err = nil }()
		if _, err := action.Execute(ctx, identity, valid); !errors.Is(err, agentrunaction.ErrCustomerReplyGenerationFailed) {
			t.Fatalf("generation error = %v", err)
		}
	})
	t.Run("周期已关闭", func(t *testing.T) {
		if _, err := closeSession.Execute(ctx, identity, conversationID); err != nil {
			t.Fatal(err)
		}
		_, err := action.Execute(ctx, identity, valid)
		if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflictError.Reason != conversationaction.ConflictReasonServiceSessionNotReplyable {
			t.Fatalf("closed error = %v", err)
		}
	})
	t.Run("可用 AI 员工", func(t *testing.T) {
		listAgents := agentrunaction.NewListServiceReplyAgentsQuery(db)
		containsCreated := func() bool {
			agents, err := listAgents.Execute(ctx, identity)
			if err != nil {
				t.Fatal(err)
			}
			return slices.ContainsFunc(agents, func(agent agentrunaction.ServiceReplyAgent) bool {
				return agent.IdentityID == created.IdentityID && agent.DisplayName == "回复建议助手"
			})
		}
		if !containsCreated() {
			t.Fatal("active agent with managed configuration is not listed")
		}
		if _, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", created.IdentityID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusActive).Where("identity_id = ?", created.IdentityID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}()
		if containsCreated() {
			t.Fatal("inactive agent is listed")
		}
	})
	t.Run("Telegram 渠道不可外发", func(t *testing.T) {
		f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
		input := agentrunaction.ServiceReplySuggestionsInput{
			ConversationID: f.run.ConversationID, AgentIdentityID: created.IdentityID,
			Mode: domain.ServiceReplyModeReply, Tone: domain.ServiceReplyToneKeep,
		}
		for _, statement := range []string{"UPDATE channels SET enabled = false WHERE id = ?", "UPDATE telegram_channel_settings SET bot_id = NULL WHERE channel_id = ?"} {
			if _, err := db.ExecContext(ctx, statement, f.channel.ID); err != nil {
				t.Fatal(err)
			}
			calls := len(generator.requests)
			_, err := action.Execute(ctx, identity, input)
			if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflictError.Reason != conversationaction.ConflictReasonChannelOutboundUnavailable || len(generator.requests) != calls {
				t.Fatalf("%s: error = %v, generator calls = %d", statement, err, len(generator.requests)-calls)
			}
			if _, err := db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channel.ID); err != nil {
				t.Fatal(err)
			}
		}
	})
}
