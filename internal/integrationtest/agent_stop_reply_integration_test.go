//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testAgentReplyStopping 验证停止消息、连续输入、幂等重放和会话隔离。
func testAgentReplyStopping(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	first, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	_, otherRun := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks))
	_, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "补充输入"})
	require.NoError(t, err)
	executor := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	for range 2 {
		status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
		require.NoError(t, err)
		require.Equal(t, domain.AgentRunStatusCancelled, status)
	}
	stopped := assertStoppedAgentReply(t, ctx, db, run.ID, 2, 0)
	// 核验已停止任务的重放结果和失败回调幂等性。
	require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("late failure")))
	_, err = executor.StopAgentReply(ctx, identity, run.ConversationID, otherRun.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross conversation stop")
	otherMember := newChatLockUser(t, db, identity)
	_, err = executor.StopAgentReply(ctx, otherMember, run.ConversationID, run.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "other member stop")
	outsider := newNavigationFixture(t)
	_, err = executor.StopAgentReply(ctx, outsider.owner, run.ConversationID, run.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross workspace stop")
	page, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: run.ConversationID})
	require.NoError(t, err)
	require.Len(t, page.Messages, 3)
	require.Equal(t, stopped.ID, page.Messages[2].ID)
	require.Nil(t, page.Messages[2].AgentProcess)
	summary, err := inboxaction.NewLoadInboxQuery(db).LoadAgentConversation(ctx, identity, run.ConversationID)
	require.NoError(t, err)
	require.NotNil(t, summary.LastMessageType)
	require.Equal(t, domain.MessageTypeAgentCancelled, *summary.LastMessageType)
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用停止", ReplyToMessageID: stopped.ID})
	require.Error(t, err, "stopped notice accepted as reference")
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "继续"})
	require.NoError(t, err)
	var next servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&next).Where("agr.conversation_id = ? AND agr.status = ?", run.ConversationID, domain.AgentRunStatusQueued).Scan(ctx))
	require.Equal(t, int64(3), next.InputStartSeq)
	_, err = executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
	require.NoError(t, err)
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 3)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if assert.Len(t, claimed.Messages, 3, "stopping altered text history") {
			assert.Equal(t, first.Message.ID, claimed.Messages[0].ID, "stopping altered text history")
		}
		for _, message := range claimed.Messages {
			assert.NotEqual(t, stopped.ID, message.ID, "stopped notice entered model context")
		}
		return agentruntime.RunResult{Content: "继续后的回复", EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: next.ID}))
	status, err := executor.StopAgentReply(ctx, identity, run.ConversationID, next.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AgentRunStatusSucceeded, status, "completed stop")
	require.NoError(t, db.NewSelect().Model(&otherRun).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusQueued), otherRun.Status, "other conversation changed")
	t.Run("失败先完成", func(t *testing.T) {
		_, failed := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		require.NoError(t, executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: failed.ID}, errors.New("model failed first")))
		status, err := executor.StopAgentReply(ctx, identity, failed.ConversationID, failed.ID)
		require.NoError(t, err)
		require.Equal(t, domain.AgentRunStatusFailed, status, "failed stop")
		count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ? AND msg.type = ?", failed.ConversationID, domain.MessageTypeAgentCancelled).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count, "stopped messages after failure")
	})
	t.Run("运行中断与迟到输出", func(t *testing.T) { testStopRunningAgentReply(t, db, identity, agentIdentityID, tasks) })
	t.Run("停止与发送并发", func(t *testing.T) { testStopAgentReplyWithSend(t, db, identity, agentIdentityID, tasks) })
	t.Run("停用后仍可停止", func(t *testing.T) {
		_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", agentID).Exec(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusActive).Where("id = ?", agentID).Exec(ctx)
		})
		_, err = executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
		require.NoError(t, err)
		assertStoppedAgentReply(t, ctx, db, run.ID, 1, 0)
	})
}

// assertStoppedAgentReply 核对停止消息与运行边界、触发绑定和已保留的过程内容条数。
func assertStoppedAgentReply(t *testing.T, ctx context.Context, db *bun.DB, runID string, end int64, wantBlocks int) servermodels.Message {
	t.Helper()
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusCancelled), run.Status)
	require.NotNil(t, run.ErrorCode)
	require.Equal(t, string(domain.AgentRunErrorCodeUserCancelled), *run.ErrorCode)
	require.NotNil(t, run.InputEndSeq)
	require.Equal(t, end, *run.InputEndSeq)
	require.NotNil(t, run.ResponseMessageID)
	var messages []servermodels.Message
	require.NoError(t, db.NewSelect().Model(&messages).Where("msg.idempotency_key = ?", "agent:"+runID).Scan(ctx))
	require.Len(t, messages, 1, "result count")
	message := messages[0]
	require.Equal(t, *run.ResponseMessageID, message.ID)
	require.Equal(t, string(domain.MessageTypeAgentCancelled), message.Type)
	require.Empty(t, message.Body)
	require.NotNil(t, message.SenderParticipantID)
	count, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).Where("ai.agent_run_id = ?", runID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, end-run.InputStartSeq+1, count, "trigger count")
	count, err = db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", runID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, wantBlocks, int(count), "partial blocks")
	return message
}

// testStopRunningAgentReply 验证停止运行后，执行实例经订阅的运行结束通知协作式中断，忽略中断的迟到结果和失败回调均收敛。
func testStopRunningAgentReply(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	startTestPublisher(t)
	for _, lateSuccess := range []bool{false, true} {
		stopRunningAgentReply(t, db, identity, agentIdentityID, tasks, lateSuccess)
	}
}

// stopRunningAgentReply 停止一次执行中的运行，执行中的尝试经消息总线收到运行结束通知。
func stopRunningAgentReply(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks, lateSuccess bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	claimed := make(chan struct{})
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		input, err := feed.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		close(claimed)
		<-ctx.Done()
		// 中断时已产生的过程内容随结果一并返回，迟到的成功结果同样携带。
		partial := agentruntime.RunResult{
			Usage: agentcontract.Usage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13},
			Blocks: runBlocks([]agentcontract.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "停止前的思考"},
			}}),
		}
		if lateSuccess {
			partial.Content, partial.EndSeq = "迟到的回复", input.EndSeq
			return partial, nil
		}
		return partial, ctx.Err()
	}}
	executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	stopper := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	finished := make(chan error, 1)
	go func() { finished <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
	waitChatSignal(t, ctx, claimed)
	_, err := stopper.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
	require.NoError(t, err)
	require.NoError(t, waitChatResult(t, ctx, finished))
	assertStoppedAgentReply(t, ctx, db, run.ID, 1, 1)
	// 主动停止的运行保留中断前的过程，成员可按运行编号读取。
	process, err := processquery.NewGetRunProcessQuery(db).Execute(ctx, identity, run.ID)
	require.NoError(t, err)
	require.Len(t, process.Blocks, 1)
	require.Equal(t, "停止前的思考", process.Blocks[0].Payload.Text)
	require.Equal(t, 13, process.Usage.TotalTokens)
}

// testStopAgentReplyWithSend 用会话锁屏障验证停止前后提交的新消息边界。
func testStopAgentReplyWithSend(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	for _, sendFirst := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
		executor := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		gate := newChatQueryGate(t, true, 1, func(event *bun.QueryEvent) bool {
			return strings.Contains(event.Query, "agent_lanes")
		})
		first, second := make(chan error, 1), make(chan error, 1)
		stop := func(ctx context.Context) error {
			_, err := executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID)
			return err
		}
		send := func(ctx context.Context) error {
			_, err := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "并发的新输入"})
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
			require.NoError(t, waitChatResult(t, ctx, result))
		}
		end := int64(1)
		if sendFirst {
			end = 2
		}
		assertStoppedAgentReply(t, ctx, db, run.ID, end, 0)
		var state servermodels.AgentLane
		require.NoError(t, db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx))
		require.Equal(t, end, state.ProcessedSeq)
		require.Equal(t, int64(2), state.DesiredSeq)
		count, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 2-end, count, "new run count")
	}
}
