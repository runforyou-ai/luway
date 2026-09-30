//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// createAgentLockChat 创建独立会话并返回待执行的首个 Run。
func createAgentLockChat(t *testing.T, ctx context.Context, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertask.Runtime) (directchataction.FirstAgentTextMessageResult, servermodels.AgentRun) {
	t.Helper()
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agentID, ClientMessageID: uuid.NewV7().String(), Body: "首个输入"})
	if err != nil {
		t.Fatal(err)
	}
	var run servermodels.AgentRun
	if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return first, run
}

// testAgentChatLocking 验证 AI 会话各执行阶段与真人发送共用会话锁。
func testAgentChatLocking(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertask.Runtime) {
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
			runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				claimed, err := feed.Claim(ctx, 1)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				if phase.failure {
					return agentruntime.RunResult{}, errors.New("test model failure")
				}
				return agentruntime.RunResult{Content: "首个结果", EndSeq: claimed.EndSeq}, nil
			}}
			executor := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil)
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
				_, err := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "后续输入"})
				sent <- err
			}()
			waitConversationLock(t, ctx, db, first.Conversation.ID)
			gate.open()
			if err := waitChatResult(t, ctx, executed); phase.failure != (err != nil) {
				t.Fatalf("execution=%v want failure=%v", err, phase.failure)
			}
			if err := waitChatResult(t, ctx, sent); err != nil {
				t.Fatal(err)
			}
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
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	wantStatus, wantType := domain.AgentRunStatusSucceeded, domain.MessageTypeText
	if failed {
		wantStatus, wantType = domain.AgentRunStatusFailed, domain.MessageTypeAgentError
	}
	if run.Status != string(wantStatus) || run.ResponseMessageID == nil {
		t.Fatalf("terminal run=%+v", run)
	}
	var message servermodels.Message
	if err := db.NewSelect().Model(&message).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx); err != nil || message.ConversationID != run.ConversationID || message.Type != string(wantType) {
		t.Fatalf("result=%+v err=%v", message, err)
	}
	var state servermodels.AgentLane
	if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx); err != nil || state.DesiredSeq != 2 || state.ProcessedSeq != 1 {
		t.Fatalf("input state=%+v err=%v", state, err)
	}
	count, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ? AND agr.input_start_seq = 2", run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("next runs=%d err=%v", count, err)
	}
	var cv servermodels.Conversation
	if err := db.NewSelect().Model(&cv).Where("cv.id = ?", run.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var latest servermodels.Message
	if err := db.NewSelect().Model(&latest).Where("msg.conversation_id = ?", cv.ID).OrderExpr("msg.message_seq DESC").Limit(1).Scan(ctx); err != nil || cv.LastMessageID == nil || *cv.LastMessageID != latest.ID {
		t.Fatalf("summary=%+v latest=%+v err=%v", cv, latest, err)
	}
	count, err = db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", cv.ID).Count(ctx)
	if err != nil || count != 3 {
		t.Fatalf("messages=%d err=%v", count, err)
	}
}

// testAgentWaitsForSender 验证 Agent 按真人发送事务、输入状态的顺序取锁。
func testAgentWaitsForSender(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertask.Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, run := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"conversation_user_states"`)
	})
	sent, executed := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "发送先完成"})
		sent <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		return agentruntime.RunResult{Content: "结果", EndSeq: claimed.EndSeq}, err
	}}
	go func() {
		executed <- agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID})
	}()
	waitConversationLock(t, ctx, db, first.Conversation.ID)
	gate.open()
	for _, result := range []<-chan error{sent, executed} {
		if err := waitChatResult(t, ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	assertAgentLockResult(t, ctx, db, run, false)
}

// testAgentAcceptedInputs 验证停用或归档前已提交的排队和运行中输入继续完成。
func testAgentAcceptedInputs(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertask.Runtime) {
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
				send := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks))
				if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "已接受的第二条"}); err != nil {
					t.Fatal(err)
				}
				entered, release := make(chan struct{}), make(chan struct{}, 1)
				defer close(release)
				runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
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
				executor := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil)
				done := make(chan error, 1)
				if running {
					go func() { done <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
					waitChatSignal(t, ctx, entered)
				}
				if change == "停用" {
					if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(ctx, identity, agentID, domain.IdentityStatusInactive); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(context.Background(), identity, agentID, domain.IdentityStatusActive); err != nil {
							t.Error(err)
						}
					})
				} else if _, err := db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).Where("id = ?", first.Conversation.ID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "此时不能接受"}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
					t.Fatalf("send after %s=%v", change, err)
				}
				if running {
					release <- struct{}{}
					if err := waitChatResult(t, ctx, done); err != nil {
						t.Fatal(err)
					}
				} else if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
					t.Fatal(err)
				}
				assertAgentLockResult(t, ctx, db, run, false)
				var next servermodels.AgentRun
				if err := db.NewSelect().Model(&next).Where("agr.conversation_id = ? AND agr.status = ?", first.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: next.ID}); err != nil {
					t.Fatal(err)
				}
				var state servermodels.AgentLane
				if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", first.Conversation.ID).Scan(ctx); err != nil || state.ProcessedSeq != 2 {
					t.Fatalf("accepted inputs not consumed: %+v err=%v", state, err)
				}
			})
		}
	}
}

// testAgentParallelConversations 验证同一 Agent 的另一会话不等待当前会话的业务锁。
func testAgentParallelConversations(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertask.Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, firstRun := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	second, secondRun := createAgentLockChat(t, ctx, db, identity, agentID, tasks)
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, `"agent_lanes"`) && strings.Contains(event.Query, "FOR UPDATE")
	})
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
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
	executor := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil)
	done := make(chan error, 1)
	go func() {
		done <- executor.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: firstRun.ID})
	}()
	waitChatSignal(t, ctx, gate.reached)
	if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: secondRun.ID}); err != nil {
		t.Fatal(err)
	}
	gate.open()
	if err := waitChatResult(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	for _, run := range []servermodels.AgentRun{firstRun, secondRun} {
		var message servermodels.Message
		if err := db.NewSelect().Model(&message).Where("msg.idempotency_key = ?", "agent:"+run.ID).Scan(ctx); err != nil || message.ConversationID != run.ConversationID || message.Body != run.ID {
			t.Fatalf("cross-conversation result=%+v err=%v", message, err)
		}
	}
}
