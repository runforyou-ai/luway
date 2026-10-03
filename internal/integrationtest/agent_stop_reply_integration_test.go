//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// testAgentReplyStopping 验证停止消息、连续输入、幂等重放和会话隔离。
func testAgentReplyStopping(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertask.Runtime) {
	ctx := context.Background()
	first, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	_, otherRun := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	send := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks))
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "补充输入"}); err != nil {
		t.Fatal(err)
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	for range 2 {
		status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
		if err != nil || status != domain.AgentRunStatusCancelled {
			t.Fatalf("stop=%s %v", status, err)
		}
	}
	stopped := assertStoppedAgentReply(t, ctx, db, run.ID, 2, 0)
	// 核验已停止任务的重放结果和失败回调幂等性。
	if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("late failure")); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.StopAgentReply(ctx, identity, run.ConversationID, otherRun.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("cross conversation stop=%v", err)
	}
	otherMember := newChatLockUser(t, db, identity)
	if _, err := executor.StopAgentReply(ctx, otherMember, run.ConversationID, run.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("other member stop=%v", err)
	}
	outsider := newNavigationFixture(t)
	if _, err := executor.StopAgentReply(ctx, outsider.owner, run.ConversationID, run.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("cross organization stop=%v", err)
	}
	page, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: run.ConversationID})
	if err != nil || len(page.Messages) != 3 || page.Messages[2].ID != stopped.ID || page.Messages[2].AgentProcess != nil {
		t.Fatalf("stopped history=%+v %v", page, err)
	}
	summary, err := inboxaction.NewLoadInboxQuery(db).LoadAgentConversation(ctx, identity, run.ConversationID)
	if err != nil || summary.LastMessageType == nil || *summary.LastMessageType != domain.MessageTypeAgentCancelled {
		t.Fatalf("stopped summary=%+v %v", summary, err)
	}
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用停止", ReplyToMessageID: stopped.ID}); err == nil {
		t.Fatal("stopped notice accepted as reference")
	}
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "继续"}); err != nil {
		t.Fatal(err)
	}
	var next servermodels.AgentRun
	if err := db.NewSelect().Model(&next).Where("agr.conversation_id = ? AND agr.status = ?", run.ConversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil || next.InputStartSeq != 3 {
		t.Fatalf("next run=%+v %v", next, err)
	}
	if _, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID); err != nil {
		t.Fatal(err)
	}
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 3)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if len(claimed.Messages) != 3 || claimed.Messages[0].ID != first.Message.ID {
			t.Error("stopping altered text history")
		}
		for _, message := range claimed.Messages {
			if message.ID == stopped.ID {
				t.Error("stopped notice entered model context")
			}
		}
		return agentruntime.RunResult{Content: "继续后的回复", EndSeq: claimed.EndSeq}, nil
	}}
	if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: next.ID}); err != nil {
		t.Fatal(err)
	}
	if status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, next.ID); err != nil || status != domain.AgentRunStatusSucceeded {
		t.Fatalf("completed stop=%s %v", status, err)
	}
	if err := db.NewSelect().Model(&otherRun).WherePK().Scan(ctx); err != nil || otherRun.Status != string(domain.AgentRunStatusQueued) {
		t.Fatalf("other conversation changed=%+v %v", otherRun, err)
	}
	t.Run("失败先完成", func(t *testing.T) {
		_, failed := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		if err := executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: failed.ID}, errors.New("model failed first")); err != nil {
			t.Fatal(err)
		}
		if status, err := executor.StopAgentReply(ctx, identity, failed.ConversationID, failed.ID); err != nil || status != domain.AgentRunStatusFailed {
			t.Fatalf("failed stop=%s %v", status, err)
		}
		count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ? AND msg.type = ?", failed.ConversationID, domain.MessageTypeAgentCancelled).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("stopped messages after failure=%d %v", count, err)
		}
	})
	t.Run("运行中断与迟到输出", func(t *testing.T) { testStopRunningAgentReply(t, db, identity, agentIdentityID, tasks) })
	t.Run("停止与发送并发", func(t *testing.T) { testStopAgentReplyWithSend(t, db, identity, agentIdentityID, tasks) })
	t.Run("停用后仍可停止", func(t *testing.T) {
		_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		if _, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", agentID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusActive).Where("id = ?", agentID).Exec(ctx)
		})
		if _, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID); err != nil {
			t.Fatal(err)
		}
		assertStoppedAgentReply(t, ctx, db, run.ID, 1, 0)
	})
}

// assertStoppedAgentReply 核对停止消息与运行边界、触发绑定和已保留的过程内容条数。
func assertStoppedAgentReply(t *testing.T, ctx context.Context, db *bun.DB, runID string, end int64, wantBlocks int) servermodels.Message {
	t.Helper()
	var run servermodels.AgentRun
	if err := db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusCancelled) || run.ErrorCode == nil || *run.ErrorCode != string(domain.AgentRunErrorCodeUserCancelled) || run.InputEndSeq == nil || *run.InputEndSeq != end || run.ResponseMessageID == nil {
		t.Fatalf("stopped run=%+v", run)
	}
	var messages []servermodels.Message
	if err := db.NewSelect().Model(&messages).Where("msg.idempotency_key = ?", "agent:"+runID).Scan(ctx); err != nil || len(messages) != 1 {
		t.Fatalf("result count=%d %v", len(messages), err)
	}
	message := messages[0]
	if message.ID != *run.ResponseMessageID || message.Type != string(domain.MessageTypeAgentCancelled) || message.Body != "" || message.SenderParticipantID == nil {
		t.Fatalf("stopped message=%+v", message)
	}
	count, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).Where("ai.agent_run_id = ?", runID).Count(ctx)
	if err != nil || int64(count) != end-run.InputStartSeq+1 {
		t.Fatalf("trigger count=%d %v", count, err)
	}
	count, err = db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", runID).Count(ctx)
	if err != nil || count != wantBlocks {
		t.Fatalf("partial blocks=%d want %d %v", count, wantBlocks, err)
	}
	return message
}

// testStopRunningAgentReply 验证本实例或另一实例停止运行后，执行实例经运行结束通知协作式中断，忽略中断的迟到结果和失败回调均收敛。
func testStopRunningAgentReply(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime) {
	publisher := startTestPublisher(t, servertest.NATSConfig(t, "test_run_ended_"+strings.ReplaceAll(uuid.NewV7().String(), "-", "")))
	for _, remote := range []bool{false, true} {
		for _, lateSuccess := range []bool{false, true} {
			stopRunningAgentReply(t, db, identity, agentIdentityID, tasks, publisher, remote, lateSuccess)
		}
	}
}

// stopRunningAgentReply 停止一次执行中的运行：remote 为真时执行实例只经 NATS 接收运行结束通知，否则只经本进程登记直接接收。
func stopRunningAgentReply(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime, publisher *realtime.Publisher, remote, lateSuccess bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	claimed := make(chan struct{})
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		input, err := feed.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		close(claimed)
		<-ctx.Done()
		// 中断时已产生的过程内容随结果一并返回，迟到的成功结果同样携带。
		partial := agentruntime.RunResult{
			Usage: agentruntime.Usage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13},
			Blocks: []agentruntime.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentruntime.BlockPayload{Text: "停止前的思考"},
			}},
		}
		if lateSuccess {
			partial.Content, partial.EndSeq = "迟到的回复", input.EndSeq
			return partial, nil
		}
		return partial, ctx.Err()
	}}
	executor := agentrunaction.NewExecuteAction(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	if remote {
		unsubscribe, err := publisher.SubscribeAgentRunEnded(executor.CancelRunContext)
		if err != nil {
			t.Fatal(err)
		}
		defer unsubscribe()
	} else {
		defer realtime.HandleAgentRunEnded(executor.CancelRunContext)()
	}
	stopper := agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	finished := make(chan error, 1)
	go func() { finished <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
	waitChatSignal(t, ctx, claimed)
	if _, err := stopper.StopAgentReply(ctx, identity, run.ConversationID, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, ctx, finished); err != nil {
		t.Fatal(err)
	}
	assertStoppedAgentReply(t, ctx, db, run.ID, 1, 1)
	// 主动停止的运行保留中断前的过程，成员可按运行编号读取。
	process, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(ctx, identity, run.ID)
	if err != nil || len(process.Blocks) != 1 || process.Blocks[0].Payload.Text != "停止前的思考" || process.Usage.TotalTokens != 13 {
		t.Fatalf("stopped agent run process = %#v, error = %v", process, err)
	}
}

// testStopAgentReplyWithSend 用会话锁屏障验证停止前后提交的新消息边界。
func testStopAgentReplyWithSend(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertask.Runtime) {
	for _, sendFirst := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		executor := agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil)
		gate := newChatQueryGate(t, true, 1, func(event *bun.QueryEvent) bool {
			return strings.Contains(event.Query, "agent_lanes")
		})
		first, second := make(chan error, 1), make(chan error, 1)
		stop := func(ctx context.Context) error {
			_, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
			return err
		}
		send := func(ctx context.Context) error {
			_, err := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "并发的新输入"})
			return err
		}
		before, after := stop, send
		if sendFirst {
			before, after = send, stop
		}
		go func() { first <- before(context.WithValue(ctx, chatQueryGateKey{}, gate)) }()
		waitChatSignal(t, ctx, gate.reached)
		go func() { second <- after(ctx) }()
		waitChatDatabaseLock(t, ctx, db, `FROM "users"`, identity.User.ID)
		gate.open()
		for _, result := range []chan error{first, second} {
			if err := waitChatResult(t, ctx, result); err != nil {
				t.Fatal(err)
			}
		}
		end := int64(1)
		if sendFirst {
			end = 2
		}
		assertStoppedAgentReply(t, ctx, db, run.ID, end, 0)
		var state servermodels.AgentLane
		if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx); err != nil || state.ProcessedSeq != end || state.DesiredSeq != 2 {
			t.Fatalf("stop/send state=%+v %v", state, err)
		}
		count, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
		if err != nil || count != int(2-end) {
			t.Fatalf("new run count=%d %v", count, err)
		}
	}
}
