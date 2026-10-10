//go:build server

package integrationtest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "你是售后客服"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "AI 写回复", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{})
	visitor := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "上一轮的问题"}
	earlier, err := receive.Execute(ctx, visitor)
	require.NoError(t, err)
	conversationID := earlier.Conversation.ID
	closeSession := servicesessionaction.NewCloseServiceSessionAction(db, newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer)
	_, err = closeSession.Execute(ctx, identity, conversationID)
	require.NoError(t, err)
	visitor.ConversationID, visitor.ClientMessageID, visitor.Body = &conversationID, uuid.NewV7().String(), "订单还没发货"
	current, err := receive.Execute(ctx, visitor)
	require.NoError(t, err)
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我来帮您查询",
	})
	require.NoError(t, err)
	// 生成上下文只包含对客消息。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "内部备注：这是重点客户",
		Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err)
	messageCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	require.NoError(t, err)

	generator := &testCustomerReplyGenerator{result: agentruntime.ReplyCandidatesResult{Candidates: []string{"您好，已为您加急处理。", "马上帮您催促仓库发货。"}}}
	action := agentrunaction.NewGenerateServiceReplySuggestionsAction(db, generator, testModelInvoker(db), testAttachmentReader(db))
	valid := agentrunaction.ServiceReplySuggestionsInput{
		ConversationID: conversationID, AgentIdentityID: created.IdentityID,
		Mode: domain.ServiceReplyModeRewrite, Tone: domain.ServiceReplyToneFriendly,
		Draft: "  帮您催一下  ", ReplyToMessageID: current.Message.ID,
	}
	candidates, err := action.Execute(ctx, identity, valid)
	require.NoError(t, err)
	require.Equal(t, generator.result.Candidates, candidates)
	require.Len(t, generator.requests, 1)
	request := generator.requests[0]
	require.Len(t, request.History, 2)
	require.Equal(t, agentcontract.MessageRoleUser, request.History[0].Role)
	require.Equal(t, "订单还没发货", request.History[0].Content)
	require.Equal(t, agentcontract.MessageRoleAssistant, request.History[1].Role)
	require.Equal(t, "我来帮您查询", request.History[1].Content)
	require.NotNil(t, request.Model.New)
	require.Equal(t, 4096, request.Model.MaxOutputTokens)
	require.True(t, strings.HasPrefix(request.Instruction, "你是售后客服"), request.Instruction)
	require.Contains(t, request.Instruction, `{"candidates":`)
	for _, expected := range []string{"改写客服草稿", "友好", `回复针对的引用消息：{"sender":"customer","content":"订单还没发货"}`, `客服草稿："帮您催一下"`} {
		require.Contains(t, request.Task, expected)
	}
	runCount, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	require.NoError(t, err)
	afterCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", conversationID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, runCount)
	require.Equal(t, messageCount, afterCount)

	// 写回复模式忽略草稿。
	reply := valid
	reply.Mode, reply.Draft, reply.ReplyToMessageID = domain.ServiceReplyModeReply, "", ""
	_, err = action.Execute(ctx, identity, reply)
	require.NoError(t, err)
	task := generator.requests[len(generator.requests)-1].Task
	require.NotContains(t, task, "客服草稿")
	require.NotContains(t, task, "引用消息")

	t.Run("引用消息不可用", func(t *testing.T) {
		input := valid
		input.ReplyToMessageID = uuid.NewV7().String()
		_, err := action.Execute(ctx, identity, input)
		var conflictError *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflictError)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflictError.Reason)
	})
	t.Run("AI 员工停用", func(t *testing.T) {
		setAgentStatus := func(status domain.IdentityStatus) {
			_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", status).Where("identity_id = ?", created.IdentityID).Exec(ctx)
			require.NoError(t, err)
		}
		setAgentStatus(domain.IdentityStatusInactive)
		defer setAgentStatus(domain.IdentityStatusActive)
		_, err := action.Execute(ctx, identity, valid)
		require.ErrorIs(t, err, agentrunaction.ErrAgentUnavailable)
	})
	t.Run("其他负责人", func(t *testing.T) {
		_, err := db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).Set("assignee_identity_id = ?", created.IdentityID).Where("id = ?", current.Conversation.ServiceSessionID).Exec(ctx)
		require.NoError(t, err)
		defer func() {
			_, err := db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).Set("assignee_identity_id = ?", identity.WorkspaceIdentity.ID).Where("id = ?", current.Conversation.ServiceSessionID).Exec(ctx)
			require.NoError(t, err)
		}()
		_, err = action.Execute(ctx, identity, valid)
		var conflictError *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflictError)
		require.Equal(t, conversationaction.ConflictReasonServiceSessionOwned, conflictError.Reason)
	})
	t.Run("周期已关闭", func(t *testing.T) {
		_, err := closeSession.Execute(ctx, identity, conversationID)
		require.NoError(t, err)
		_, err = action.Execute(ctx, identity, valid)
		var conflictError *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflictError)
		require.Equal(t, conversationaction.ConflictReasonServiceSessionNotReplyable, conflictError.Reason)
	})
	t.Run("可用 AI 员工", func(t *testing.T) {
		listAgents := agentrunaction.NewListServiceReplyAgentsQuery(db)
		containsCreated := func() bool {
			agents, err := listAgents.Execute(ctx, identity)
			require.NoError(t, err)
			return slices.ContainsFunc(agents, func(agent agentrunaction.ServiceReplyAgent) bool {
				return agent.IdentityID == created.IdentityID && agent.DisplayName == "回复建议助手"
			})
		}
		require.True(t, containsCreated(), "active agent with managed configuration is not listed")
		_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", created.IdentityID).Exec(ctx)
		require.NoError(t, err)
		defer func() {
			_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusActive).Where("identity_id = ?", created.IdentityID).Exec(ctx)
			require.NoError(t, err)
		}()
		require.False(t, containsCreated(), "inactive agent is listed")
	})
	t.Run("Telegram 渠道不可外发", func(t *testing.T) {
		f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
		input := agentrunaction.ServiceReplySuggestionsInput{
			ConversationID: f.run.ConversationID, AgentIdentityID: created.IdentityID,
			Mode: domain.ServiceReplyModeReply, Tone: domain.ServiceReplyToneKeep,
		}
		for _, statement := range []string{"UPDATE channels SET enabled = false WHERE id = ?", "UPDATE channels SET provider_account_id = NULL WHERE id = ?"} {
			_, err := db.ExecContext(ctx, statement, f.channel.ID)
			require.NoError(t, err)
			calls := len(generator.requests)
			_, err = action.Execute(ctx, identity, input)
			var conflictError *conversationaction.ConflictError
			require.ErrorAs(t, err, &conflictError, statement)
			require.Equal(t, conversationaction.ConflictReasonChannelOutboundUnavailable, conflictError.Reason, statement)
			require.Len(t, generator.requests, calls, statement)
			_, err = db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channel.ID)
			require.NoError(t, err)
		}
	})
}
