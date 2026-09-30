//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type isolatedChatRuntime struct{ expected map[string][]string }

// Run 校验每个 Run 只接收所属会话的历史。
func (r *isolatedChatRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
	triggers, err := feed.Peek(ctx, 0)
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

type failingMessageScheduler struct {
	inner          *agentrunaction.Scheduler
	taskIDs        []string
	conversationID string
	failure        error
}

// Schedule 在真实输入和任务创建后返回失败以验证整个首发事务回滚。
func (s *failingMessageScheduler) Schedule(ctx context.Context, db bun.IDB, organizationID, conversationID, agentID, revisionID, messageID, senderSubjectID string, kind domain.AgentInputKind) error {
	if err := s.inner.Schedule(ctx, db, organizationID, conversationID, agentID, revisionID, messageID, senderSubjectID, kind); err != nil {
		return err
	}
	return s.failAfterSchedule(ctx, db, conversationID, messageID)
}

// ScheduleCustomerAuto 在真实访客输入和任务创建后返回失败。
func (s *failingMessageScheduler) ScheduleCustomerAuto(ctx context.Context, db bun.IDB, organizationID, conversationID, sessionID, messageID string) (bool, error) {
	scheduled, err := s.inner.ScheduleCustomerAuto(ctx, db, organizationID, conversationID, sessionID, messageID)
	if err != nil {
		return false, err
	}
	return scheduled, s.failAfterSchedule(ctx, db, conversationID, messageID)
}

// failAfterSchedule 记录消息、摘要及任务在同一事务中的关联，再注入回滚错误。
func (s *failingMessageScheduler) failAfterSchedule(ctx context.Context, db bun.IDB, conversationID, messageID string) error {
	s.conversationID = conversationID
	if err := db.NewSelect().TableExpr("task_runs tr").ColumnExpr("tr.id").
		Join("JOIN task_outbox tob ON tob.task_run_id = tr.id").
		Join("JOIN agent_runs agr ON tr.idempotency_key = 'agent:' || agr.id::text").
		Join("JOIN agent_inputs ai ON ai.lane_id = agr.lane_id AND ai.organization_id = agr.organization_id").
		Join("JOIN conversations cv ON cv.id = agr.conversation_id AND cv.last_message_id = ai.source_message_id").
		Join("LEFT JOIN service_sessions ss ON ss.id = agr.scope_id AND ss.organization_id = agr.organization_id AND agr.scope_kind = ?", domain.AgentExecutionScopeServiceSession).
		Where("agr.scope_kind = ? OR ss.last_message_id = ai.source_message_id", domain.AgentExecutionScopeConversation).
		Where("agr.conversation_id = ? AND ai.source_message_id = ?", conversationID, messageID).Scan(ctx, &s.taskIDs); err != nil {
		return err
	}
	return s.failure
}

// TestAgentConversations 验证独立 AI 会话的创建幂等、上下文及访问边界。
func TestAgentConversations(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "独立会话助手",
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "按当前会话回答"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	start := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler)
	firstInput := directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "任务甲"}
	if found, err := db.NewSelect().Model((*servermodels.Conversation)(nil)).Where("id = ?", firstInput.ConversationID).Exists(ctx); err != nil || found {
		t.Fatalf("draft persisted: %v %v", found, err)
	}
	// 同一首发并发重试只确认同一条消息。
	results := make([]directchataction.FirstAgentTextMessageResult, 4)
	failures := make([]error, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i], failures[i] = start.Execute(ctx, identity, firstInput) })
	}
	wg.Wait()
	for i, result := range results {
		if failures[i] != nil || result.Conversation.ID != firstInput.ConversationID || result.Message.ID != results[0].Message.ID || result.Message.ClientMessageID == nil || *result.Message.ClientMessageID != firstInput.ClientMessageID {
			t.Fatalf("retry %d: %+v %v", i, result, failures[i])
		}
	}
	first := results[0]
	assertMemberClientAssociation(t, db, identity, first.Conversation.ID, first.Message.ID, firstInput.ClientMessageID)
	secondInput := firstInput
	secondInput.ConversationID, secondInput.ClientMessageID, secondInput.Body = uuid.NewV7().String(), uuid.NewV7().String(), "任务乙"
	second, err := start.Execute(ctx, identity, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.Conversation.ID, second.Conversation.ID} {
		for _, table := range []string{"messages", "agent_runs", "agent_conversations"} {
			count, err := db.NewSelect().TableExpr(table).Where("conversation_id = ?", id).Count(ctx)
			if err != nil || count != 1 {
				t.Fatalf("%s rows=%d err=%v", table, count, err)
			}
		}
		inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
			Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
			Where("al.conversation_id = ?", id).Count(ctx)
		if err != nil || inputCount != 1 {
			t.Fatalf("agent_inputs rows=%d err=%v", inputCount, err)
		}
	}
	altered := firstInput
	altered.Body = "被改动的重试"
	if _, err := start.Execute(ctx, identity, altered); err == nil {
		t.Fatal("changed idempotent body accepted")
	}
	reused := secondInput
	reused.ConversationID = uuid.NewV7().String()
	if _, err := start.Execute(ctx, identity, reused); err == nil {
		t.Fatal("message identifier reused across conversations")
	}
	// 执行两个会话，模型输入和输出分别归属自己的 Conversation。
	runs := make([]servermodels.AgentRun, 0)
	if err := db.NewSelect().Model(&runs).Where("agr.conversation_id IN (?, ?)", first.Conversation.ID, second.Conversation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	runtime := &isolatedChatRuntime{expected: map[string][]string{}}
	for _, run := range runs {
		body := "任务甲"
		if run.ConversationID == second.Conversation.ID {
			body = "任务乙"
		}
		runtime.expected[run.ID] = []string{body}
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil)
	for _, run := range runs {
		if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		var saved servermodels.AgentRun
		if err := db.NewSelect().Model(&saved).Where("agr.id = ?", run.ID).Scan(ctx); err != nil || saved.Status != string(domain.AgentRunStatusSucceeded) {
			t.Fatalf("run failed: %+v %v", saved, err)
		}
	}
	// 按编号读取独立摘要和批量资格，核验两个 AI 会话各自的最新结果。
	details, err := inboxaction.NewLoadInboxQuery(db).ReadByIDs(ctx, identity, []string{first.Conversation.ID, second.Conversation.ID}, &inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || len(details) != 2 {
		t.Fatalf("agent summaries=%+v err=%v", details, err)
	}
	for index, detail := range details {
		if !detail.MatchesQuery || detail.Conversation == nil || detail.Conversation.Agent == nil || detail.Conversation.Agent.AgentRunStatus == nil || *detail.Conversation.Agent.AgentRunStatus != domain.AgentRunStatusSucceeded {
			t.Fatalf("agent detail %d=%+v", index, detail)
		}
	}
	// Agent 已回复后重放首发，确认消息不变，摘要保留当前水位和运行状态。
	replay, err := start.Execute(ctx, identity, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	summary := replay.Conversation
	if replay.Message.ID != first.Message.ID || summary.LastMessageID == nil || *summary.LastMessageID == first.Message.ID || summary.LastReadMessageID == nil || *summary.LastReadMessageID != first.Message.ID || summary.UnreadCount != 1 || summary.Agent.AgentRunStatus == nil || *summary.Agent.AgentRunStatus != domain.AgentRunStatusSucceeded {
		t.Fatalf("replayed summary = %+v, agent = %+v", summary, summary.Agent)
	}
	testAgentConversationAccess(t, db, identity, scheduler, first, second)
	rollbackInput := firstInput
	rollbackInput.ConversationID, rollbackInput.ClientMessageID = uuid.NewV7().String(), uuid.NewV7().String()
	failing := &failingMessageScheduler{inner: scheduler, failure: errors.New("test scheduling failure")}
	if _, err := directchataction.NewSendFirstAgentTextMessageAction(db, failing).Execute(ctx, identity, rollbackInput); !errors.Is(err, failing.failure) {
		t.Fatalf("expected scheduling failure: %v", err)
	}
	for _, table := range []string{"agent_conversations", "conversation_participants", "messages", "agent_lanes", "agent_runs"} {
		count, err := db.NewSelect().TableExpr(table).Where("conversation_id = ?", rollbackInput.ConversationID).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
	inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
		Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
		Where("al.conversation_id = ?", rollbackInput.ConversationID).Count(ctx)
	if err != nil || inputCount != 0 {
		t.Fatalf("rollback agent_inputs rows=%d err=%v", inputCount, err)
	}
	if exists, err := db.NewSelect().Model((*servermodels.Conversation)(nil)).Where("id = ?", rollbackInput.ConversationID).Exists(ctx); err != nil || exists {
		t.Fatalf("empty conversation survived rollback: %v %v", exists, err)
	}
	if len(failing.taskIDs) != 1 {
		t.Fatalf("atomic task rows were not observed: %v", failing.taskIDs)
	}
	for table, column := range map[string]string{"task_runs": "id", "task_outbox": "task_run_id"} {
		count, err := db.NewSelect().TableExpr(table).Where("? IN (?)", bun.Ident(column), bun.In(failing.taskIDs)).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
	db.AddQueryHook(chatQueryHook{})
	t.Run("主动停止回复", func(t *testing.T) {
		testAgentReplyStopping(t, db, identity, agent.ID, agent.IdentityID, tasks)
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
	t.Run("本机设备执行", func(t *testing.T) {
		testDeviceAgentRuns(t, db, identity, agent, tasks)
	})
}

// testAgentConversationAccess 验证多会话列表、阅读状态、引用和参与者访问范围。
func testAgentConversationAccess(t *testing.T, db *bun.DB, identity *servermodels.Identity, scheduler *agentrunaction.Scheduler, first, second directchataction.FirstAgentTextMessageResult) {
	t.Helper()
	ctx := context.Background()
	send := directchataction.NewSendAgentTextMessageAction(db, scheduler)
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "跨会话引用", ReplyToMessageID: second.Message.ID}); err == nil {
		t.Fatal("cross conversation reference accepted")
	}
	history := conversationaction.NewListConversationMessagesQuery(db)
	for _, result := range []directchataction.FirstAgentTextMessageResult{first, second} {
		page, err := history.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: result.Conversation.ID})
		if err != nil || len(page.Messages) != 2 || page.Messages[1].Body != "答复："+result.Message.Body || page.Messages[1].ClientMessageID != nil || page.Messages[0].ClientMessageID == nil || *page.Messages[0].ClientMessageID != *result.Message.ClientMessageID {
			t.Fatalf("history: %+v %v", page, err)
		}
	}
	mark := conversationaction.NewMarkConversationReadAction(db)
	page, _ := history.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	if _, err := mark.Execute(ctx, identity, first.Conversation.ID, page.Messages[1].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(db).Execute(ctx, identity, second.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	unreadMark := conversationaction.NewUpdateConversationUnreadMarkAction(db)
	if err := unreadMark.Execute(ctx, identity, first.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	rowsPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		if row.ID != first.Conversation.ID && row.ID != second.Conversation.ID {
			continue
		}
		found++
		if row.LastActivityAt == nil || row.Agent == nil || row.Direct != nil || row.Agent.AgentIdentityID != first.Conversation.Agent.AgentIdentityID {
			t.Fatalf("AI inbox payload: %+v", row)
		}
		if row.ID == first.Conversation.ID && (row.UnreadCount != 0 || row.Muted || !row.MarkedUnread) {
			t.Fatalf("first read state: %+v", row)
		}
		if row.ID == second.Conversation.ID && (row.UnreadCount != 1 || !row.Muted || row.MarkedUnread) {
			t.Fatalf("second read state: %+v", row)
		}
	}
	if found != 2 {
		t.Fatalf("AI inbox rows=%d", found)
	}
	if err := unreadMark.Execute(ctx, identity, first.Conversation.ID, false); err != nil {
		t.Fatal(err)
	}
	outsider := newNavigationFixture(t)
	for _, actor := range []*servermodels.Identity{outsider.owner, outsider.member} {
		if _, err := history.Execute(ctx, actor, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("outside history: %v", err)
		}
		if _, err := send.Execute(ctx, actor, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "无权发送"}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("outside send: %v", err)
		}
	}
	if _, err := directchataction.NewSendFirstDirectTextMessageAction(db).Execute(ctx, identity, directchataction.FirstDirectTextMessageInput{TargetIdentityID: first.Conversation.Agent.AgentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: "旧入口"}); !errors.Is(err, conversationaction.ErrDirectTargetNotFound) {
		t.Fatalf("direct accepted Agent: %v", err)
	}
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "继续任务甲"}); err != nil {
		t.Fatal(err)
	}
}
