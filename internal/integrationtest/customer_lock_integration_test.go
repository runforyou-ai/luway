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
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// createCustomerLockRun 创建自动路由到指定 Agent 的网站会话及首个输入。
func createCustomerLockRun(t *testing.T, ctx context.Context, db *bun.DB, identity *models.Identity, agentID string, tasks *servertest.Tasks) (customerchataction.ReceiveWebsiteCustomerMessageResult, customerchataction.WebsiteCustomerTextMessageInput, models.AgentRun) {
	t.Helper()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "客服锁序", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "首个输入"}
	first, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{}).Execute(ctx, input)
	require.NoError(t, err)
	var run models.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx))
	input.ConversationID, input.ClientMessageID, input.Body = &first.Conversation.ID, uuid.NewV7().String(), "后续输入"
	return first, input, run
}

// testCustomerAgentLocking 验证客服 Agent 每个执行阶段都先持有 Conversation 锁。
func testCustomerAgentLocking(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertest.Tasks) {
	// 独立 Bun 包装只安装本组屏障，底层连接池仍由测试夹具管理。
	db = bun.NewDB(db.DB, db.Dialect())
	db.AddQueryHook(chatQueryHook{})
	for _, phase := range []struct {
		name              string
		occurrence        int
		failure, finalize bool
	}{
		{name: "开始", occurrence: 1}, {name: "输入认领", occurrence: 2}, {name: "成功写回", occurrence: 3},
		{name: "失败写回", occurrence: 3, failure: true}, {name: "最终失败", occurrence: 1, finalize: true},
	} {
		t.Run("客服锁序/"+phase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			first, input, run := createCustomerLockRun(t, ctx, db, identity, agentID, tasks)
			gate := newChatQueryGate(t, false, phase.occurrence, func(event *bun.QueryEvent) bool {
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `"agent_lanes"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				claim, err := feed.Claim(ctx, 1)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				if phase.failure {
					return agentruntime.RunResult{}, errors.New("受控失败")
				}
				return agentruntime.RunResult{Content: "客服结果", EndSeq: claim.EndSeq}, nil
			}}
			executor := newTestAgentRun(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
			executed, received := make(chan error, 1), make(chan error, 1)
			go func() {
				gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
				if phase.finalize {
					executed <- executor.FinalizeFailure(gated, agentrunaction.RunInput{RunID: run.ID}, errors.New("最终失败"))
				} else {
					executed <- executor.Execute(gated, agentrunaction.RunInput{RunID: run.ID})
				}
			}()
			waitChatSignal(t, ctx, gate.reached)
			go func() {
				_, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{}).Execute(ctx, input)
				received <- err
			}()
			waitConversationLock(t, ctx, db, first.Conversation.ID)
			gate.open()
			err := waitChatResult(t, ctx, executed)
			require.Equal(t, phase.failure, err != nil, "execution=%v", err)
			require.NoError(t, waitChatResult(t, ctx, received))
			if phase.failure || phase.finalize {
				assertCustomerFailureHandoff(t, ctx, db, run)
			} else {
				assertAgentLockResult(t, ctx, db, run, false)
			}
			assertCustomerLockSummary(t, ctx, db, first.Conversation.ID)
		})
	}
	for _, change := range []string{"领取", "转交", "关闭续开"} {
		for _, aiFirst := range []bool{false, true} {
			name := "人工先提交"
			if aiFirst {
				name = "AI先提交"
			}
			t.Run("客服并发/"+change+"/"+name, func(t *testing.T) { testCustomerLateResult(t, db, identity, agentID, tasks, change, aiFirst) })
		}
	}
}

// assertCustomerFailureHandoff 核对失败的客服运行已转交人工：对客通知为主结果，等待会话锁的后续消息在交接后由人工处理，AI 输入队列保持结算边界。
func assertCustomerFailureHandoff(t *testing.T, ctx context.Context, db *bun.DB, run models.AgentRun) {
	t.Helper()
	require.NoError(t, db.NewSelect().Model(&run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusFailed), run.Status)
	require.NotNil(t, run.ResponseMessageID)
	require.NotNil(t, run.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeHandoff), *run.Outcome)
	require.NotNil(t, run.HandoffSettledSeq)
	require.Equal(t, int64(1), *run.HandoffSettledSeq)
	var notice models.Message
	require.NoError(t, db.NewSelect().Model(&notice).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx))
	require.Equal(t, string(domain.MessageTypeText), notice.Type)
	var state models.AgentLane
	require.NoError(t, db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx))
	require.Equal(t, int64(1), state.DesiredSeq)
	require.Equal(t, int64(1), state.ProcessedSeq)
	active, err := db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status IN (?)", run.ConversationID,
		bun.List([]domain.AgentRunStatus{domain.AgentRunStatusQueued, domain.AgentRunStatusRunning})).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, active, "active runs")
	// 两条客户消息、内部错误消息、转人工事件和对客通知。
	count, err := db.NewSelect().Model((*models.Message)(nil)).Where("msg.conversation_id = ?", run.ConversationID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(5), count, "messages")
}

// assertCustomerLockSummary 核对会话摘要、当前周期摘要及消息的周期归属。
func assertCustomerLockSummary(t *testing.T, ctx context.Context, db *bun.DB, conversationID string) {
	t.Helper()
	var cv models.Conversation
	require.NoError(t, db.NewSelect().Model(&cv).Where("cv.id = ?", conversationID).Scan(ctx))
	var session models.ServiceSession
	require.NoError(t, db.NewSelect().Model(&session).Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.workspace_id = ss.workspace_id").Where("svc.conversation_id = ?", conversationID).Scan(ctx))
	var message models.Message
	require.NoError(t, db.NewSelect().Model(&message).Where("msg.id = ?", session.LastMessageID).Scan(ctx))
	require.NotNil(t, cv.LastMessageID)
	require.Equal(t, message.ID, *cv.LastMessageID)
	require.NotNil(t, message.ServiceSessionID)
	require.Equal(t, session.ID, *message.ServiceSessionID)
}

// testCustomerLateResult 控制人工操作与 AI 写回事务顺序，并验证 Run 的客服周期归属。
func testCustomerLateResult(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertest.Tasks, change string, aiFirst bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, input, run := createCustomerLockRun(t, ctx, db, identity, agentID, tasks)
	// AI 先提交时在持有会话锁后暂停；人工先提交时在申请会话锁前暂停。
	gate := newChatQueryGate(t, !aiFirst, 3, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
	})
	model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claim, err := feed.Claim(ctx, 1)
		return agentruntime.RunResult{Content: "迟到结果", EndSeq: claim.EndSeq}, err
	}}
	executor := newTestAgentRun(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	// 使用独立协调器验证跨进程的事务校验。
	coordinator := newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	executed, managed := make(chan error, 1), make(chan error, 1)
	go func() {
		executed <- executor.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: run.ID})
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, testEnqueuer).Execute(ctx, identity, first.Conversation.ID)
		if err == nil {
			switch change {
			case "转交":
				_, err = servicesessionaction.NewTransferServiceSessionAction(db, coordinator, agentrunaction.NewScheduler(tasks), testEnqueuer).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agentID})
			case "关闭续开":
				_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer).Execute(ctx, identity, first.Conversation.ID)
				if err == nil {
					_, err = customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, servertest.DisabledMail{}).Execute(ctx, input)
				}
			}
		}
		managed <- err
	}()
	if aiFirst {
		waitConversationLock(t, ctx, db, first.Conversation.ID)
		gate.open()
	} else {
		require.NoError(t, waitChatResult(t, ctx, managed))
		gate.open()
	}
	require.NoError(t, waitChatResult(t, ctx, executed))
	if aiFirst {
		require.NoError(t, waitChatResult(t, ctx, managed))
	}
	// 核验重复执行及最终失败回调的结果和终态幂等性。
	require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("重复终态")))
	require.NoError(t, db.NewSelect().Model(&run).WherePK().Scan(ctx))
	wantStatus := domain.AgentRunStatusCancelled
	wantCount := 0
	if aiFirst {
		wantStatus = domain.AgentRunStatusSucceeded
		wantCount = 1
	}
	count, err := db.NewSelect().Model((*models.Message)(nil)).Where("msg.idempotency_key = ?", "agent:"+run.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, wantCount, int(count), "messages")
	require.Equal(t, string(wantStatus), run.Status)
	count, err = db.NewSelect().Model((*models.ConversationParticipant)(nil)).Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id").Where("cp.conversation_id = ? AND cs.source_id = ?", first.Conversation.ID, agentID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, wantCount, int(count), "AI participants")
	assertCustomerLockSummary(t, ctx, db, first.Conversation.ID)
}

// TestCustomerInboundAndManagementLocks 验证入站、客服管理和成员回复都在周期锁之前持有会话锁。
func TestCustomerInboundAndManagementLocks(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"网站入站", "成员回复", "领取", "转交", "关闭", "重开"} {
		t.Run(operation, func(t *testing.T) {
			f := newCustomerReadFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			coordinator := newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
			if operation == "转交" {
				_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
				require.NoError(t, err)
			}
			if operation == "重开" {
				_, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
				require.NoError(t, err)
			}
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, true, 1, func(event *bun.QueryEvent) bool {
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "service_conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			operated, contended := make(chan error, 1), make(chan error, 1)
			go func() {
				gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
				var err error
				switch operation {
				case "网站入站":
					_, err = f.visitorMessage(gated, "竞争入站")
				case "成员回复":
					_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(gated, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "成员回复"})
				case "领取":
					_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(gated, f.owner, f.conversationID)
				case "转交":
					_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, nil, testEnqueuer).Execute(gated, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID})
				case "关闭":
					_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(gated, f.owner, f.conversationID)
				case "重开":
					_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(gated, f.owner, f.conversationID)
				}
				operated <- err
			}()
			waitChatSignal(t, ctx, gate.reached)
			go func() {
				contended <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
					_, err := tx.ExecContext(ctx, "SELECT id FROM conversations WHERE id = ? FOR UPDATE", f.conversationID)
					return err
				})
			}()
			waitChatDatabaseLock(t, ctx, f.db, "FROM conversations", f.conversationID)
			gate.open()
			for _, done := range []<-chan error{operated, contended} {
				require.NoError(t, waitChatResult(t, ctx, done))
			}
			assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
		})
	}
}

// TestWebsiteFirstMessageConverges 验证相同首发幂等键竞争渠道身份时只提交一组业务记录。
func TestWebsiteFirstMessageConverges(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f.db.AddQueryHook(chatQueryHook{})
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:abcdef0123456789abcdef0123456789", ClientMessageID: uuid.NewV7().String(), Body: "并发首发"}
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"channel_identities"`)
	})
	results := make([]customerchataction.ReceiveWebsiteCustomerMessageResult, 2)
	done := make(chan error, 2)
	go func() {
		var err error
		results[0], err = f.receive.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), input)
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		var err error
		results[1], err = f.receive.Execute(ctx, input)
		done <- err
	}()
	// 后到的首发在分配联系人编号时等待先到的事务提交。
	waitChatDatabaseLock(t, ctx, f.db, "last_contact_number", f.owner.Workspace.ID)
	gate.open()
	for range 2 {
		require.NoError(t, waitChatResult(t, ctx, done))
	}
	require.Equal(t, results[0].Message.ID, results[1].Message.ID)
	require.Equal(t, results[0].Conversation.ID, results[1].Conversation.ID)
	for _, table := range []string{"messages", "service_sessions", "service_conversations", "channel_conversations"} {
		count, err := f.db.NewSelect().TableExpr(table).Where("conversation_id = ?", results[0].Conversation.ID).Count(ctx)
		require.NoError(t, err, table)
		require.Equal(t, int64(1), count, table)
	}
}

// TestCustomerReopenAndInboundConverge 验证显式重开与客户续开竞争时只保留一个开放周期。
func TestCustomerReopenAndInboundConverge(t *testing.T) {
	t.Parallel()
	for _, reopenFirst := range []bool{false, true} {
		name := "客户先续开"
		if reopenFirst {
			name = "成员先重开"
		}
		t.Run(name, func(t *testing.T) {
			f := newCustomerReadFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			closed, err := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
			require.NoError(t, err)
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			reopened, received := make(chan error, 1), make(chan error, 1)
			reopen := func(c context.Context) {
				_, err := servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(c, f.owner, f.conversationID)
				reopened <- err
			}
			receive := func(c context.Context) { _, err := f.visitorMessage(c, "续开输入"); received <- err }
			gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
			if reopenFirst {
				go reopen(gated)
			} else {
				go receive(gated)
			}
			waitChatSignal(t, ctx, gate.reached)
			if reopenFirst {
				go receive(ctx)
			} else {
				go reopen(ctx)
			}
			waitConversationLock(t, ctx, f.db, f.conversationID)
			gate.open()
			err = waitChatResult(t, ctx, reopened)
			if reopenFirst {
				require.NoError(t, err)
			} else {
				var conflict *conversationaction.ConflictError
				require.ErrorAs(t, err, &conflict)
				require.Equal(t, servicesessionaction.ConflictReasonServiceSessionAlreadyOpen, conflict.Reason)
			}
			require.NoError(t, waitChatResult(t, ctx, received))
			var sessions []models.ServiceSession
			require.NoError(t, f.db.NewSelect().Model(&sessions).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx))
			require.Len(t, sessions, 1)
			require.Equal(t, reopenFirst, sessions[0].ID == closed.ID, "sessions=%+v", sessions)
			assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
		})
	}
}

// TestTelegramInboundCredentialLock 验证入站事件处理与停用渠道按渠道行锁串行：入站先取得锁时消息写入，停用先提交时入站事件丢弃。
func TestTelegramInboundCredentialLock(t *testing.T) {
	t.Parallel()
	for _, disableFirst := range []bool{false, true} {
		name := "入站先提交"
		if disableFirst {
			name = "停用先提交"
		}
		t.Run(name, func(t *testing.T) {
			f := newChannelDeliveryFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
				if disableFirst {
					return event.Operation() == "UPDATE" && strings.Contains(event.Query, `"telegram_channel_settings"`)
				}
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
			received, disabled := make(chan error, 1), make(chan error, 1)
			receive := func(c context.Context) {
				received <- receiver.Execute(c, f.channelID, telegramUpdate{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{SenderID: 12345, ChatID: 12345, MessageID: 2, Body: "等待中的入站", OriginatedAt: time.Now().UTC()}})
			}
			disable := func(c context.Context) {
				_, err := telegramaction.NewUpdateChannelStatusAction(f.db, connectiontest.NewRunner(time.Second), &telegramBotAPIFake{}, testEnqueuer).Execute(c, f.owner, f.channelID, false)
				disabled <- err
			}
			gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
			if disableFirst {
				go disable(gated)
			} else {
				go receive(gated)
			}
			waitChatSignal(t, ctx, gate.reached)
			if disableFirst {
				go receive(ctx)
			} else {
				go disable(ctx)
			}
			waitChatDatabaseLock(t, ctx, f.db, `FROM "channels"`, f.channelID)
			gate.open()
			require.NoError(t, waitChatResult(t, ctx, received))
			require.NoError(t, waitChatResult(t, ctx, disabled))
			count, err := f.db.NewSelect().Model((*models.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
			want := 2
			if disableFirst {
				want = 1
			}
			require.NoError(t, err)
			require.Equal(t, want, int(count), "messages")
			assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
		})
	}
}

// testCustomerSharedAgentSubject 验证同一 Agent 在不同客户会话首次回复时共用唯一聊天主体。
func testCustomerSharedAgentSubject(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertest.Tasks) {
	t.Helper()
	db = bun.NewDB(db.DB, db.Dialect())
	db.AddQueryHook(chatQueryHook{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"chat_subjects"`)
	})
	done := make(chan error, 2)
	for i := range 2 {
		_, _, run := createCustomerLockRun(t, ctx, db, identity, agentID, tasks)
		model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			claim, err := feed.Claim(ctx, 1)
			return agentruntime.RunResult{Content: "共同主体的回复", EndSeq: claim.EndSeq}, err
		}}
		executionCtx := ctx
		if i == 0 {
			executionCtx = context.WithValue(ctx, chatQueryGateKey{}, gate)
		}
		go func() {
			done <- newTestAgentRun(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(executionCtx, agentrunaction.RunInput{RunID: run.ID})
		}()
		if i == 0 {
			waitChatSignal(t, ctx, gate.reached)
		}
	}
	waitChatDatabaseLock(t, ctx, db, `INSERT INTO "chat_subjects"`, agentID)
	gate.open()
	for range 2 {
		require.NoError(t, waitChatResult(t, ctx, done))
	}
	count, err := db.NewSelect().Model((*models.ChatSubject)(nil)).Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, agentID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "subjects")
}
