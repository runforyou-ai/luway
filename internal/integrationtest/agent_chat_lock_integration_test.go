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
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// createAgentLockChat 创建独立会话并返回待执行的首个 Run。
func createAgentLockChat(t *testing.T, ctx context.Context, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertest.Tasks) (directchataction.FirstAgentTextMessageResult, servermodels.AgentRun) {
	t.Helper()
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agentID, ClientMessageID: uuid.NewV7().String(), Body: "首个输入"})
	require.NoError(t, err)
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx))
	return first, run
}

// testAgentChatLocking 验证 AI 会话各执行阶段与真人发送共用会话锁。
func testAgentChatLocking(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertest.Tasks) {
	for _, phase := range []struct {
		name       string
		occurrence int
		failure    bool
		finalize   bool
	}{
		{name: "开始", occurrence: 1},
		{name: "输入认领", occurrence: 2},
		{name: "成功写回", occurrence: 3},
		{name: "失败写回", occurrence: 3, failure: true},
		{name: "最终失败", occurrence: 1, finalize: true},
	} {
		t.Run(phase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			first, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
			gate := newChatQueryGate(t, false, phase.occurrence, func(event *bun.QueryEvent) bool {
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `"agent_lanes"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				claimed, err := feed.Claim(ctx, 1)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				if phase.failure {
					return agentruntime.RunResult{}, errors.New("test model failure")
				}
				return agentruntime.RunResult{Content: "首个结果", EndSeq: claimed.EndSeq}, nil
			}}
			executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
			executed, sent := make(chan error, 1), make(chan error, 1)
			go func() {
				ctx := context.WithValue(ctx, chatQueryGateKey{}, gate)
				if phase.finalize {
					executed <- executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("test exhausted task"))
				} else {
					executed <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID})
				}
			}()
			waitChatSignal(t, ctx, gate.reached)
			go func() {
				_, err := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "后续输入"})
				sent <- err
			}()
			waitConversationLock(t, ctx, db, first.Conversation.ID)
			gate.open()
			err := waitChatResult(t, ctx, executed)
			require.Equal(t, phase.failure, err != nil, "execution=%v", err)
			require.NoError(t, waitChatResult(t, ctx, sent))
			assertAgentLockResult(t, ctx, db, run, phase.failure || phase.finalize)
		})
	}
	t.Run("发送先持锁", func(t *testing.T) {
		testAgentWaitsForSender(t, db, identity, agentIdentityID, tasks)
	})
	t.Run("停用和归档保留已提交输入", func(t *testing.T) {
		testAgentAcceptedInputs(t, db, identity, agentID, agentIdentityID, tasks)
	})
	t.Run("不同会话独立运行", func(t *testing.T) {
		testAgentParallelConversations(t, db, identity, agentIdentityID, tasks)
	})
}

// assertAgentLockResult 核对结果消息、消费水位、后续 Run 和会话摘要一起收敛。
func assertAgentLockResult(t *testing.T, ctx context.Context, db *bun.DB, run servermodels.AgentRun, failed bool) {
	t.Helper()
	require.NoError(t, db.NewSelect().Model(&run).WherePK().Scan(ctx))
	wantStatus, wantType := domain.AgentRunStatusSucceeded, domain.MessageTypeText
	if failed {
		wantStatus, wantType = domain.AgentRunStatusFailed, domain.MessageTypeAgentError
	}
	require.Equal(t, string(wantStatus), run.Status)
	require.NotNil(t, run.ResponseMessageID)
	var message servermodels.Message
	require.NoError(t, db.NewSelect().Model(&message).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx))
	require.Equal(t, run.ConversationID, message.ConversationID)
	require.Equal(t, string(wantType), message.Type)
	var state servermodels.AgentLane
	require.NoError(t, db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx))
	require.Equal(t, int64(2), state.DesiredSeq)
	require.Equal(t, int64(1), state.ProcessedSeq)
	count, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ? AND agr.input_start_seq = 2", run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "next runs")
	var cv servermodels.Conversation
	require.NoError(t, db.NewSelect().Model(&cv).Where("cv.id = ?", run.ConversationID).Scan(ctx))
	var latest servermodels.Message
	require.NoError(t, db.NewSelect().Model(&latest).Where("msg.conversation_id = ?", cv.ID).OrderExpr("msg.message_seq DESC").Limit(1).Scan(ctx))
	require.NotNil(t, cv.LastMessageID)
	require.Equal(t, latest.ID, *cv.LastMessageID)
	count, err = db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", cv.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3), count, "messages")
}

// testAgentWaitsForSender 验证 Agent 按真人发送事务、输入状态的顺序取锁。
func testAgentWaitsForSender(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertest.Tasks) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, run := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"conversation_user_states"`)
	})
	sent, executed := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "发送先完成"})
		sent <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		return agentruntime.RunResult{Content: "结果", EndSeq: claimed.EndSeq}, err
	}}
	go func() {
		executed <- newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID})
	}()
	waitConversationLock(t, ctx, db, first.Conversation.ID)
	gate.open()
	for _, result := range []<-chan error{sent, executed} {
		require.NoError(t, waitChatResult(t, ctx, result))
	}
	assertAgentLockResult(t, ctx, db, run, false)
}

// testAgentAcceptedInputs 验证停用或归档前已提交的排队和运行中输入继续完成。
func testAgentAcceptedInputs(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertest.Tasks) {
	for _, change := range []string{"停用", "归档"} {
		for _, running := range []bool{false, true} {
			name := change + "/排队"
			if running {
				name = change + "/运行中"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				first, run := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
				send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks))
				_, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "已接受的第二条"})
				require.NoError(t, err)
				entered, release := make(chan struct{}), make(chan struct{}, 1)
				defer close(release)
				runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
					through := int64(2)
					if request.RunID == run.ID {
						through = 1
					}
					claimed, err := feed.Claim(ctx, through)
					if running && request.RunID == run.ID {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return agentruntime.RunResult{}, ctx.Err()
						}
					}
					return agentruntime.RunResult{Content: "已接受输入的结果", EndSeq: claimed.EndSeq}, err
				}}
				executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
				done := make(chan error, 1)
				if running {
					go func() { done <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
					waitChatSignal(t, ctx, entered)
				}
				if change == "停用" {
					_, err := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agentID, domain.IdentityStatusInactive)
					require.NoError(t, err)
					t.Cleanup(func() {
						_, err := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(context.Background(), identity, agentID, domain.IdentityStatusActive)
						assert.NoError(t, err)
					})
				} else {
					_, err := db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).Where("id = ?", first.Conversation.ID).Exec(ctx)
					require.NoError(t, err)
				}
				_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "此时不能接受"})
				require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "send after %s", change)
				if running {
					release <- struct{}{}
					require.NoError(t, waitChatResult(t, ctx, done))
				} else {
					require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
				}
				assertAgentLockResult(t, ctx, db, run, false)
				var next servermodels.AgentRun
				require.NoError(t, db.NewSelect().Model(&next).Where("agr.conversation_id = ? AND agr.status = ?", first.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx))
				require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: next.ID}))
				var state servermodels.AgentLane
				require.NoError(t, db.NewSelect().Model(&state).Where("al.conversation_id = ?", first.Conversation.ID).Scan(ctx))
				require.Equal(t, int64(2), state.ProcessedSeq, "accepted inputs not consumed")
			})
		}
	}
}

// testAgentParallelConversations 验证同一 Agent 的另一会话不等待当前会话的业务锁。
func testAgentParallelConversations(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertest.Tasks) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, firstRun := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	second, secondRun := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, `"agent_lanes"`) && strings.Contains(event.Query, "FOR UPDATE")
	})
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		expectedID := first.Message.ID
		if request.RunID == secondRun.ID {
			expectedID = second.Message.ID
		}
		if err == nil && (len(claimed.Messages) != 1 || claimed.Messages[0].ID != expectedID) {
			err = errors.New("another conversation entered agent context")
		}
		return agentruntime.RunResult{Content: request.RunID, EndSeq: claimed.EndSeq}, err
	}}
	executor := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	done := make(chan error, 1)
	go func() {
		done <- executor.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: firstRun.ID})
	}()
	waitChatSignal(t, ctx, gate.reached)
	require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: secondRun.ID}))
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, done))
	for _, run := range []servermodels.AgentRun{firstRun, secondRun} {
		var message servermodels.Message
		require.NoError(t, db.NewSelect().Model(&message).Where("msg.idempotency_key = ?", "agent:"+run.ID).Scan(ctx))
		require.Equal(t, run.ConversationID, message.ConversationID)
		require.Equal(t, run.ID, message.Body)
	}
}
