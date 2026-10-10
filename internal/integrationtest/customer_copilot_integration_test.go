//go:build server

package integrationtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceCopilotThreads 验证 Copilot 线程的创建、多人提问、背景资料、停止回复、会话隔离与实时受众。
func TestServiceCopilotThreads(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "Copilot 助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "你是售后专家"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "Copilot 线程", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	inbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "包裹显示签收但没收到",
	})
	require.NoError(t, err)
	customerID := inbound.Conversation.ID
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: customerID, ClientMessageID: uuid.NewV7().String(), Body: "我来帮您核实物流",
	})
	require.NoError(t, err)
	customerMessageCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", customerID).Count(ctx)
	require.NoError(t, err)
	colleague := newChatLockUser(t, db, identity)
	outsider := newNavigationFixture(t).owner
	startThread := directchataction.NewSendFirstServiceCopilotMessageAction(db, testEnqueuer, scheduler)
	ask := directchataction.NewSendServiceCopilotTextMessageAction(db, testEnqueuer, scheduler)
	listThreads := directchataction.NewListServiceCopilotThreadsQuery(db)
	listMessages := conversationaction.NewListConversationMessagesQuery(db)

	threadID := uuid.NewV7().String()
	firstInput := directchataction.FirstServiceCopilotMessageInput{
		ThreadID: threadID, ServedConversationID: customerID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "这个客户之前退过款吗",
	}
	first, err := startThread.Execute(ctx, identity, firstInput)
	require.NoError(t, err)
	type threadFields struct{ ID, Title, AgentIdentityID, CreatedByIdentityID string }
	require.Equal(t,
		threadFields{threadID, firstInput.Body, created.IdentityID, identity.WorkspaceIdentity.ID},
		threadFields{first.Thread.ID, first.Thread.Title, first.Thread.AgentIdentityID, first.Thread.CreatedByIdentityID},
	)
	replayed, err := startThread.Execute(ctx, identity, firstInput)
	require.NoError(t, err)
	require.Equal(t, first.Message.ID, replayed.Message.ID)
	mismatched := firstInput
	mismatched.ServedConversationID = uuid.NewV7().String()
	var conflict *conversationaction.ConflictError
	_, err = startThread.Execute(ctx, identity, mismatched)
	require.ErrorAs(t, err, &conflict)
	// 重放核对线程的固定归属：换 AI 员工或由其他成员以同一线程编号首发均为幂等冲突。
	mismatchedAgent := firstInput
	mismatchedAgent.AgentIdentityID = uuid.NewV7().String()
	_, err = startThread.Execute(ctx, identity, mismatchedAgent)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
	// 附件首发 Copilot 线程的重放同样核对所属服务会话，使用另一个客服会话。
	otherInbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "发票怎么开",
	})
	require.NoError(t, err)
	sendAttachment := directchataction.NewSendAttachmentMessageAction(db, testEnqueuer, scheduler)
	attachmentInput := directchataction.AttachmentMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: created.IdentityID, ServedConversationID: otherInbound.Conversation.ID,
		ClientMessageID: uuid.NewV7().String(), FileID: uploadedAttachment(t, db, identity, "copilot.png", "image/png"),
	}
	_, err = sendAttachment.Execute(ctx, identity, attachmentInput)
	require.NoError(t, err)
	mismatchedAttachment := attachmentInput
	mismatchedAttachment.ServedConversationID = customerID
	_, err = sendAttachment.Execute(ctx, identity, mismatchedAttachment)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
	_, err = ask.Execute(ctx, colleague, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: uuid.NewV7().String(), Body: "物流单号能查到吗"})
	require.NoError(t, err)
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", threadID).Scan(ctx))
	var kinds []string
	require.NoError(t, db.NewSelect().Table("agent_inputs").Column("kind").Where("lane_id = ?", run.LaneID).Order("input_seq").Scan(ctx, &kinds))
	require.Equal(t, string(domain.AgentRunStatusQueued), run.Status)
	require.Equal(t, string(domain.AgentExecutionScopeConversation), run.ScopeKind)
	require.Equal(t, threadID, run.ScopeID)
	require.Equal(t, []string{string(domain.AgentInputKindCopilot), string(domain.AgentInputKindCopilot)}, kinds)

	threads, err := listThreads.Execute(ctx, colleague, customerID)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Equal(t, threadID, threads[0].ID)
	require.Equal(t, domain.IdentityStatusActive, threads[0].AgentStatus)
	inboxPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(ctx, colleague, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	assertInboxConversationPresence(t, inboxPage.Conversations, threadID, false)
	search, err := inboxaction.NewLoadInboxQuery(db).Search(ctx, identity, inboxaction.SearchInput{Text: "物流单号", Range: inboxaction.SearchRangeReadable})
	require.NoError(t, err)
	for _, message := range search.Messages {
		require.NotEqual(t, threadID, message.Conversation.ID, "copilot thread message appeared in search")
	}
	stateCount, err := db.NewSelect().Table("conversation_user_states").Where("conversation_id = ?", threadID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, stateCount)

	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 2)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		instruction := request.Assignment.Instruction
		assert.True(t, strings.HasPrefix(instruction, "你是企业「"), "copilot instruction = %q", instruction)
		for _, expected := range []string{"\n\n你是售后专家", "customer_conversation_background", "customer-reply", "与客户最近消息相同的语言"} {
			assert.Contains(t, instruction, expected)
		}
		if !assert.Len(t, claimed.Messages, 3) || !assert.True(t, strings.HasPrefix(claimed.Messages[0].ID, "copilot-background:"+customerID+":"), "copilot context = %+v", claimed.Messages) {
			return agentruntime.RunResult{Content: "上下文不符", EndSeq: claimed.EndSeq}, nil
		}
		// Copilot 可检索同一客户的已关闭周期，当前进行中的周期不在检索范围内。
		if assert.NotNil(t, request.CustomerHistorySearch) && assert.Contains(t, request.Assignment.Tools, agentruntime.CustomerHistoryToolName) &&
			assert.Contains(t, request.Assignment.Instruction, "- search_customer_history：需要了解该客户") {
			found, err := request.CustomerHistorySearch(ctx, "签收")
			assert.NoError(t, err)
			assert.Empty(t, found.Sessions)
			assert.NotEmpty(t, found.Message)
		}
		background := claimed.Messages[0].Content
		for _, expected := range []string{`"kind":"customer_conversation_background"`, "包裹显示签收但没收到", "我来帮您核实物流", `"kind":"customer"`, `"kind":"member"`, `"status":"open"`} {
			assert.Contains(t, background, expected)
		}
		assert.Contains(t, claimed.Messages[1].Content, `"name":"`+identity.WorkspaceIdentity.DisplayName+`"`)
		assert.Contains(t, claimed.Messages[2].Content, `"name":"`+colleague.WorkspaceIdentity.DisplayName+`"`)
		return agentruntime.RunResult{Content: "建议先核实物流签收凭证", EndSeq: claimed.EndSeq}, nil
	}}
	executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	history, err := listMessages.Execute(ctx, colleague, conversationaction.ConversationMessageHistoryInput{ConversationID: threadID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 3)
	require.Equal(t, "建议先核实物流签收凭证", history.Messages[2].Body)
	require.NotNil(t, history.Messages[2].Sender)
	require.Equal(t, created.IdentityID, history.Messages[2].Sender.SourceID)
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", customerID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, customerMessageCount, count, "customer messages after copilot")

	// 跨企业身份不能读取线程列表、线程消息或继续提问。
	_, err = listThreads.Execute(ctx, outsider, customerID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outsider list threads")
	_, err = listMessages.Execute(ctx, outsider, conversationaction.ConversationMessageHistoryInput{ConversationID: threadID})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outsider thread history")
	_, err = ask.Execute(ctx, outsider, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: uuid.NewV7().String(), Body: "越权提问"})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outsider ask")

	feed := startRealtimeFeed(t, identity.Workspace.ID)
	_, err = ask.Execute(ctx, colleague, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: uuid.NewV7().String(), Body: "还有别的办法吗"})
	require.NoError(t, err)
	feed.expect(t, feed.customerInbox(threadID, loadConversationVersion(t, db, threadID)))
	var next servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&next).Where("agr.conversation_id = ? AND agr.status = ?", threadID, domain.AgentRunStatusQueued).Scan(ctx))
	require.Equal(t, int64(3), next.InputStartSeq)
	_, err = executor.StopServiceCopilotReply(ctx, outsider, threadID, next.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outsider stop")
	status, err := executor.StopServiceCopilotReply(ctx, identity, threadID, next.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AgentRunStatusCancelled, status)
	feed.expect(t, feed.customerInbox(threadID, loadConversationVersion(t, db, threadID)))
	count, err = db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ? AND type = ?", threadID, domain.MessageTypeAgentCancelled).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "copilot stopped messages")

	// 停用 AI 员工后线程保留只读，新提问返回 AI 员工不可用。
	_, err = db.NewUpdate().Table("agents").Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", created.IdentityID).Exec(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.NewUpdate().Table("agents").Set("status = ?", domain.IdentityStatusActive).Where("identity_id = ?", created.IdentityID).Exec(context.Background())
	})
	_, err = ask.Execute(ctx, colleague, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: uuid.NewV7().String(), Body: "停用后提问"})
	require.ErrorIs(t, err, conversationaction.ErrAgentUnavailable, "inactive agent ask")
	_, err = startThread.Execute(ctx, colleague, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: uuid.NewV7().String(), ServedConversationID: customerID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "新对话",
	})
	require.ErrorIs(t, err, conversationaction.ErrAgentUnavailable, "inactive agent new thread")
	threads, err = listThreads.Execute(ctx, identity, customerID)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Equal(t, domain.IdentityStatusInactive, threads[0].AgentStatus)
}

// TestServiceCopilotConcurrentFirstAsk 验证同一成员在他人创建的线程中并发首次提问时都成功且只加入一次参与者。
func TestServiceCopilotConcurrentFirstAsk(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "Copilot 助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "你是售后专家"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "Copilot 并发", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	inbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{}).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "订单还没发货",
	})
	require.NoError(t, err)
	threadID := uuid.NewV7().String()
	_, err = directchataction.NewSendFirstServiceCopilotMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: threadID, ServedConversationID: inbound.Conversation.ID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "发货进度如何",
	})
	require.NoError(t, err)
	colleague := newChatLockUser(t, db, identity)
	ask := directchataction.NewSendServiceCopilotTextMessageAction(db, testEnqueuer, scheduler)

	const senders = 2
	errs := make([]error, senders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = ask.Execute(ctx, colleague, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: uuid.NewV7().String(), Body: "能加急吗"})
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "sender %d", i)
	}
	participants, err := db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id").
		Where("cp.conversation_id = ? AND cs.kind = ? AND cs.source_id = ?", threadID, domain.ChatSubjectKindWorkspaceIdentity, colleague.WorkspaceIdentity.ID).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), participants)
	messages, err := db.NewSelect().TableExpr("messages AS m").
		Join("JOIN conversation_participants AS cp ON cp.workspace_id = m.workspace_id AND cp.id = m.sender_participant_id").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id").
		Where("m.conversation_id = ? AND cs.source_id = ?", threadID, colleague.WorkspaceIdentity.ID).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(senders), messages)
}
