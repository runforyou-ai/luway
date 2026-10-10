//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// isolatedChatRuntime 按会话校验运行收到的历史的测试运行时。
type isolatedChatRuntime struct{ expected map[string][]string }

// Run 校验每个 Run 只接收所属会话的历史。
func (r *isolatedChatRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
	triggers, err := pendingTriggers(ctx, feed, 0)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	expected := r.expected[request.RunID]
	if len(claimed.Messages) != len(expected) {
		return agentruntime.RunResult{}, fmt.Errorf("context length=%d expected=%d", len(claimed.Messages), len(expected))
	}
	for i, message := range claimed.Messages {
		if message.Content != expected[i] {
			return agentruntime.RunResult{}, fmt.Errorf("context leaked: %q expected=%q", message.Content, expected[i])
		}
	}
	return agentruntime.RunResult{Content: "答复：" + expected[0], EndSeq: claimed.EndSeq}, nil
}

// failingMessageScheduler 在真实调度后返回失败的 Agent 调度器。
type failingMessageScheduler struct {
	inner          *agentrunaction.Scheduler
	runIDs         []string
	conversationID string
	failure        error
}

// Schedule 在真实输入和任务创建后返回失败以验证整个首发事务回滚。
func (s *failingMessageScheduler) Schedule(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentID, revisionID, messageID, senderSubjectID string, kind domain.AgentInputKind) error {
	if err := s.inner.Schedule(ctx, db, workspaceID, conversationID, agentID, revisionID, messageID, senderSubjectID, kind); err != nil {
		return err
	}
	return s.failAfterSchedule(ctx, db, conversationID, messageID)
}

// ScheduleCustomerAuto 在真实访客输入和任务创建后返回失败。
func (s *failingMessageScheduler) ScheduleCustomerAuto(ctx context.Context, db bun.IDB, workspaceID, conversationID, sessionID, messageID string) (bool, error) {
	scheduled, err := s.inner.ScheduleCustomerAuto(ctx, db, workspaceID, conversationID, sessionID, messageID)
	if err != nil {
		return false, err
	}
	return scheduled, s.failAfterSchedule(ctx, db, conversationID, messageID)
}

// failAfterSchedule 记录同一事务中为该消息创建的 Agent 运行，再注入回滚错误。
func (s *failingMessageScheduler) failAfterSchedule(ctx context.Context, db bun.IDB, conversationID, messageID string) error {
	s.conversationID = conversationID
	if err := db.NewSelect().TableExpr("agent_runs agr").ColumnExpr("agr.id").
		Join("JOIN agent_inputs ai ON ai.lane_id = agr.lane_id AND ai.workspace_id = agr.workspace_id").
		Join("JOIN conversations cv ON cv.id = agr.conversation_id AND cv.last_message_id = ai.source_message_id").
		Join("LEFT JOIN service_sessions ss ON ss.id = agr.scope_id AND ss.workspace_id = agr.workspace_id AND agr.scope_kind = ?", domain.AgentExecutionScopeServiceSession).
		Where("agr.scope_kind = ? OR ss.last_message_id = ai.source_message_id", domain.AgentExecutionScopeConversation).
		Where("agr.conversation_id = ? AND ai.source_message_id = ?", conversationID, messageID).Scan(ctx, &s.runIDs); err != nil {
		return err
	}
	return s.failure
}

// requireNoQueuedRuns 断言登记器中没有执行指定 Agent 运行的任务。
func requireNoQueuedRuns(t *testing.T, tasks *servertest.Tasks, runIDs []string) {
	t.Helper()
	for _, task := range tasks.Queued(agentrunaction.RunActionName, "") {
		require.NotContains(t, runIDs, servertest.TaskPayload[agentrunaction.RunInput](t, task).RunID, "rolled back run task")
	}
}

// TestAgentConversations 验证独立 AI 会话的创建幂等、上下文及访问边界。
func TestAgentConversations(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "独立会话助手",
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "按当前会话回答"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	start := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler)
	firstInput := directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "任务甲"}
	found, err := db.NewSelect().Model((*servermodels.Conversation)(nil)).Where("id = ?", firstInput.ConversationID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, found, "draft persisted")
	// 同一首发并发重试只确认同一条消息。
	results := make([]directchataction.FirstAgentTextMessageResult, 4)
	failures := make([]error, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i], failures[i] = start.Execute(ctx, identity, firstInput) })
	}
	wg.Wait()
	for i, result := range results {
		require.NoError(t, failures[i], "retry %d", i)
		require.Equal(t, firstInput.ConversationID, result.Conversation.ID, "retry %d", i)
		require.Equal(t, results[0].Message.ID, result.Message.ID, "retry %d", i)
		require.NotNil(t, result.Message.ClientMessageID, "retry %d", i)
		require.Equal(t, firstInput.ClientMessageID, *result.Message.ClientMessageID, "retry %d", i)
	}
	first := results[0]
	assertMemberClientAssociation(t, db, identity, first.Conversation.ID, first.Message.ID, firstInput.ClientMessageID)
	secondInput := firstInput
	secondInput.ConversationID, secondInput.ClientMessageID, secondInput.Body = uuid.NewV7().String(), uuid.NewV7().String(), "任务乙"
	second, err := start.Execute(ctx, identity, secondInput)
	require.NoError(t, err)
	for _, id := range []string{first.Conversation.ID, second.Conversation.ID} {
		for _, table := range []string{"messages", "agent_runs", "agent_conversations"} {
			count, err := db.NewSelect().TableExpr(table).Where("conversation_id = ?", id).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, int64(1), count, "%s rows", table)
		}
		inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
			Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
			Where("al.conversation_id = ?", id).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), inputCount, "agent_inputs rows")
	}
	altered := firstInput
	altered.Body = "被改动的重试"
	_, err = start.Execute(ctx, identity, altered)
	require.Error(t, err, "changed idempotent body accepted")
	reused := secondInput
	reused.ConversationID = uuid.NewV7().String()
	_, err = start.Execute(ctx, identity, reused)
	require.Error(t, err, "message identifier reused across conversations")
	// 执行两个会话，模型输入和输出分别归属自己的 Conversation。
	runs := make([]servermodels.AgentRun, 0)
	require.NoError(t, db.NewSelect().Model(&runs).Where("agr.conversation_id IN (?, ?)", first.Conversation.ID, second.Conversation.ID).Scan(ctx))
	runtime := &isolatedChatRuntime{expected: map[string][]string{}}
	for _, run := range runs {
		body := "任务甲"
		if run.ConversationID == second.Conversation.ID {
			body = "任务乙"
		}
		runtime.expected[run.ID] = []string{body}
	}
	executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	for _, run := range runs {
		require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
		var saved servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&saved).Where("agr.id = ?", run.ID).Scan(ctx))
		require.Equal(t, string(domain.AgentRunStatusSucceeded), saved.Status, "run failed: %+v", saved)
	}
	// 按编号读取独立摘要和批量资格，核验两个 AI 会话各自的最新结果。
	details, err := inboxaction.NewLoadInboxQuery(db).ReadByIDs(ctx, identity, []string{first.Conversation.ID, second.Conversation.ID}, &inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Len(t, details, 2)
	for index, detail := range details {
		require.True(t, detail.MatchesQuery, "agent detail %d", index)
		require.NotNil(t, detail.Conversation, "agent detail %d", index)
		require.NotNil(t, detail.Conversation.Agent, "agent detail %d", index)
		require.NotNil(t, detail.Conversation.Agent.AgentRunStatus, "agent detail %d", index)
		require.Equal(t, domain.AgentRunStatusSucceeded, *detail.Conversation.Agent.AgentRunStatus, "agent detail %d", index)
	}
	// Agent 已回复后重放首发，确认消息不变，摘要保留当前水位和运行状态。
	replay, err := start.Execute(ctx, identity, firstInput)
	require.NoError(t, err)
	summary := replay.Conversation
	require.Equal(t, first.Message.ID, replay.Message.ID)
	require.NotNil(t, summary.LastMessageID)
	require.NotEqual(t, first.Message.ID, *summary.LastMessageID)
	require.NotNil(t, summary.LastReadMessageID)
	require.Equal(t, first.Message.ID, *summary.LastReadMessageID)
	require.Equal(t, 1, summary.UnreadCount)
	require.NotNil(t, summary.Agent.AgentRunStatus)
	require.Equal(t, domain.AgentRunStatusSucceeded, *summary.Agent.AgentRunStatus)
	// 重放核对 AI 聊天的固定归属，换 AI 员工为幂等冲突。
	mismatched := firstInput
	mismatched.AgentIdentityID = uuid.NewV7().String()
	var conflict *conversationaction.ConflictError
	_, err = start.Execute(ctx, identity, mismatched)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
	testAgentConversationAccess(t, db, identity, scheduler, first, second)
	rollbackInput := firstInput
	rollbackInput.ConversationID, rollbackInput.ClientMessageID = uuid.NewV7().String(), uuid.NewV7().String()
	failing := &failingMessageScheduler{inner: scheduler, failure: errors.New("test scheduling failure")}
	_, err = directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, failing).Execute(ctx, identity, rollbackInput)
	require.ErrorIs(t, err, failing.failure, "expected scheduling failure")
	for _, table := range []string{"agent_conversations", "conversation_participants", "messages", "agent_lanes", "agent_runs"} {
		count, err := db.NewSelect().TableExpr(table).Where("conversation_id = ?", rollbackInput.ConversationID).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count, "rollback %s rows", table)
	}
	inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
		Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
		Where("al.conversation_id = ?", rollbackInput.ConversationID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, inputCount, "rollback agent_inputs rows")
	exists, err := db.NewSelect().Model((*servermodels.Conversation)(nil)).Where("id = ?", rollbackInput.ConversationID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "empty conversation survived rollback")
	require.Len(t, failing.runIDs, 1, "atomic agent runs were not observed")
	requireNoQueuedRuns(t, tasks, failing.runIDs)
	db.AddQueryHook(chatQueryHook{})
	t.Run("主动停止回复", func(t *testing.T) {
		testAgentReplyStopping(t, db, identity, agent.ID, agent.IdentityID, tasks)
	})
	t.Run("运行挂起与恢复", func(t *testing.T) {
		testAgentRunSuspendAndResume(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("安全点增量写入过程", func(t *testing.T) {
		testAgentRunIncrementalSafepoint(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("恢复即挂起时通知负责人核对", func(t *testing.T) {
		testAgentRunReviewNotice(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("结束通知丢失时停止推理", func(t *testing.T) {
		testAgentRunEndedWithoutNotice(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("停止挂起的运行", func(t *testing.T) {
		testStopWaitingAgentRun(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("失败时取消未结束的调用", func(t *testing.T) {
		testFailedAgentRunCancelsToolCalls(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("禁用 AI 员工后保留会话", func(t *testing.T) {
		testDisabledAgentConversation(t, db, identity, agent.ID, agent.IdentityID, tasks)
	})
	t.Run("AI 会话事务锁序", func(t *testing.T) {
		testAgentChatLocking(t, db, identity, agent.ID, agent.IdentityID, tasks)
	})
	t.Run("运行状态实时通知", func(t *testing.T) {
		testAgentRunNotifications(t, db, identity, agent.IdentityID, tasks)
	})
	t.Run("个人 AI 员工使用电脑", func(t *testing.T) {
		testPersonalAgentComputer(t, db, identity, agent, tasks)
	})
	t.Run("服务型 AI 员工使用工作区电脑", func(t *testing.T) {
		testWorkspaceComputerAgent(t, db, identity, agent, tasks)
	})
}

// testAgentConversationAccess 验证多会话列表、阅读状态、引用和参与者访问范围。
func testAgentConversationAccess(t *testing.T, db *bun.DB, identity *servermodels.Identity, scheduler *agentrunaction.Scheduler, first, second directchataction.FirstAgentTextMessageResult) {
	t.Helper()
	ctx := context.Background()
	send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, scheduler)
	_, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "跨会话引用", ReplyToMessageID: second.Message.ID})
	require.Error(t, err, "cross conversation reference accepted")
	history := conversationaction.NewListConversationMessagesQuery(db)
	for _, result := range []directchataction.FirstAgentTextMessageResult{first, second} {
		page, err := history.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: result.Conversation.ID})
		require.NoError(t, err)
		require.Len(t, page.Messages, 2)
		require.Equal(t, "答复："+result.Message.Body, page.Messages[1].Body)
		require.Nil(t, page.Messages[1].ClientMessageID)
		require.NotNil(t, page.Messages[0].ClientMessageID)
		require.Equal(t, *result.Message.ClientMessageID, *page.Messages[0].ClientMessageID)
	}
	mark := conversationaction.NewMarkConversationReadAction(db)
	page, _ := history.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	_, err = mark.Execute(ctx, identity, first.Conversation.ID, page.Messages[1].ID, false)
	require.NoError(t, err)
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(db).Execute(ctx, identity, second.Conversation.ID, true)
	require.NoError(t, err)
	unreadMark := conversationaction.NewUpdateConversationUnreadMarkAction(db)
	require.NoError(t, unreadMark.Execute(ctx, identity, first.Conversation.ID, true))
	rowsPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	found := 0
	for _, row := range rows {
		if row.ID != first.Conversation.ID && row.ID != second.Conversation.ID {
			continue
		}
		found++
		require.NotNil(t, row.LastActivityAt)
		require.NotNil(t, row.Agent)
		require.Nil(t, row.Direct)
		require.Equal(t, first.Conversation.Agent.AgentIdentityID, row.Agent.AgentIdentityID)
		if row.ID == first.Conversation.ID {
			require.Zero(t, row.UnreadCount, "first read state")
			require.False(t, row.Muted, "first read state")
			require.True(t, row.MarkedUnread, "first read state")
		}
		if row.ID == second.Conversation.ID {
			require.Equal(t, 1, row.UnreadCount, "second read state")
			require.True(t, row.Muted, "second read state")
			require.False(t, row.MarkedUnread, "second read state")
		}
	}
	require.Equal(t, 2, found, "AI inbox rows")
	require.NoError(t, unreadMark.Execute(ctx, identity, first.Conversation.ID, false))
	outsider := newNavigationFixture(t)
	for _, actor := range []*servermodels.Identity{outsider.owner, outsider.member} {
		_, err := history.Execute(ctx, actor, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
		require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outside history")
		_, err = send.Execute(ctx, actor, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "无权发送"})
		require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "outside send")
	}
	_, err = directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(ctx, identity, directchataction.FirstDirectTextMessageInput{TargetIdentityID: first.Conversation.Agent.AgentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: "旧入口"})
	require.ErrorIs(t, err, conversationaction.ErrDirectTargetNotFound, "direct accepted Agent")
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "继续任务甲"})
	require.NoError(t, err)
}
