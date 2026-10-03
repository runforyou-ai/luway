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
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// createCustomerLockRun 创建自动路由到指定 Agent 的网站会话及首个输入。
func createCustomerLockRun(t *testing.T, ctx context.Context, db *bun.DB, identity *models.Identity, agentID string, tasks *servertask.Runtime) (customerchataction.ReceiveWebsiteCustomerMessageResult, customerchataction.WebsiteCustomerTextMessageInput, models.AgentRun) {
	t.Helper()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "客服锁序", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "首个输入"}
	first, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), nil).Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var run models.AgentRun
	if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	input.ConversationID, input.ClientMessageID, input.Body = &first.Conversation.ID, uuid.NewV7().String(), "后续输入"
	return first, input, run
}

// testCustomerAgentLocking 验证客服 Agent 每个执行阶段都先持有 Conversation 锁。
func testCustomerAgentLocking(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertask.Runtime) {
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
			model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				claim, err := feed.Claim(ctx, 1)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				if phase.failure {
					return agentruntime.RunResult{}, errors.New("受控失败")
				}
				return agentruntime.RunResult{Content: "客服结果", EndSeq: claim.EndSeq}, nil
			}}
			executor := agentrunaction.NewExecuteAction(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, nil)
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
				_, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), nil).Execute(ctx, input)
				received <- err
			}()
			waitConversationLock(t, ctx, db, first.Conversation.ID)
			gate.open()
			if err := waitChatResult(t, ctx, executed); phase.failure != (err != nil) {
				t.Fatalf("execution=%v", err)
			}
			if err := waitChatResult(t, ctx, received); err != nil {
				t.Fatal(err)
			}
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
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusFailed) || run.ResponseMessageID == nil || run.Outcome == nil ||
		*run.Outcome != string(domain.AgentRunOutcomeHandoff) || run.HandoffSettledSeq == nil || *run.HandoffSettledSeq != 1 {
		t.Fatalf("terminal run=%+v", run)
	}
	var notice models.Message
	if err := db.NewSelect().Model(&notice).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx); err != nil || notice.Type != string(domain.MessageTypeText) {
		t.Fatalf("notice=%+v err=%v", notice, err)
	}
	var state models.AgentLane
	if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx); err != nil || state.DesiredSeq != 1 || state.ProcessedSeq != 1 {
		t.Fatalf("input state=%+v err=%v", state, err)
	}
	active, err := db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status IN (?)", run.ConversationID,
		bun.In([]domain.AgentRunStatus{domain.AgentRunStatusQueued, domain.AgentRunStatusRunning})).Count(ctx)
	if err != nil || active != 0 {
		t.Fatalf("active runs=%d err=%v", active, err)
	}
	// 两条客户消息、内部错误消息、转人工事件和对客通知。
	count, err := db.NewSelect().Model((*models.Message)(nil)).Where("msg.conversation_id = ?", run.ConversationID).Count(ctx)
	if err != nil || count != 5 {
		t.Fatalf("messages=%d err=%v", count, err)
	}
}

// assertCustomerLockSummary 核对会话摘要、当前周期摘要及消息的周期归属。
func assertCustomerLockSummary(t *testing.T, ctx context.Context, db *bun.DB, conversationID string) {
	t.Helper()
	var cv models.Conversation
	if err := db.NewSelect().Model(&cv).Where("cv.id = ?", conversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var session models.ServiceSession
	if err := db.NewSelect().Model(&session).Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.organization_id = ss.organization_id").Where("svc.conversation_id = ?", conversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var message models.Message
	if err := db.NewSelect().Model(&message).Where("msg.id = ?", session.LastMessageID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if cv.LastMessageID == nil || *cv.LastMessageID != message.ID || message.ServiceSessionID == nil || *message.ServiceSessionID != session.ID {
		t.Fatalf("conversation=%+v session=%+v message=%+v", cv, session, message)
	}
}

// testCustomerLateResult 控制人工操作与 AI 写回事务顺序，并验证 Run 的客服周期归属。
func testCustomerLateResult(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertask.Runtime, change string, aiFirst bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, input, run := createCustomerLockRun(t, ctx, db, identity, agentID, tasks)
	// AI 先提交时在持有会话锁后暂停；人工先提交时在申请会话锁前暂停。
	gate := newChatQueryGate(t, !aiFirst, 3, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
	})
	model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		claim, err := feed.Claim(ctx, 1)
		return agentruntime.RunResult{Content: "迟到结果", EndSeq: claim.EndSeq}, err
	}}
	executor := agentrunaction.NewExecuteAction(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	// 使用独立协调器验证跨进程的事务校验。
	coordinator := agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil)
	executed, managed := make(chan error, 1), make(chan error, 1)
	go func() {
		executed <- executor.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: run.ID})
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, first.Conversation.ID)
		if err == nil {
			switch change {
			case "转交":
				_, err = servicesessionaction.NewTransferServiceSessionAction(db, coordinator, agentrunaction.NewScheduler(tasks), newTestTasks(db)).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agentID})
			case "关闭续开":
				_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, first.Conversation.ID)
				if err == nil {
					_, err = customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), nil).Execute(ctx, input)
				}
			}
		}
		managed <- err
	}()
	if aiFirst {
		waitConversationLock(t, ctx, db, first.Conversation.ID)
		gate.open()
	} else {
		if err := waitChatResult(t, ctx, managed); err != nil {
			t.Fatal(err)
		}
		gate.open()
	}
	if err := waitChatResult(t, ctx, executed); err != nil {
		t.Fatal(err)
	}
	if aiFirst {
		if err := waitChatResult(t, ctx, managed); err != nil {
			t.Fatal(err)
		}
	}
	// 核验重复执行及最终失败回调的结果和终态幂等性。
	if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := executor.FinalizeFailure(ctx, agentrunaction.RunInput{RunID: run.ID}, errors.New("重复终态")); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	wantStatus := domain.AgentRunStatusCancelled
	wantCount := 0
	if aiFirst {
		wantStatus = domain.AgentRunStatusSucceeded
		wantCount = 1
	}
	count, err := db.NewSelect().Model((*models.Message)(nil)).Where("msg.idempotency_key = ?", "agent:"+run.ID).Count(ctx)
	if err != nil || count != wantCount || run.Status != string(wantStatus) {
		t.Fatalf("run=%+v messages=%d err=%v", run, count, err)
	}
	count, err = db.NewSelect().Model((*models.ConversationParticipant)(nil)).Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id").Where("cp.conversation_id = ? AND cs.source_id = ?", first.Conversation.ID, agentID).Count(ctx)
	if err != nil || count != wantCount {
		t.Fatalf("AI participants=%d err=%v", count, err)
	}
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
			coordinator := agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil)
			if operation == "转交" {
				if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "重开" {
				if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
					t.Fatal(err)
				}
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
					_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(gated, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "成员回复"})
				case "领取":
					_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(gated, f.owner, f.conversationID)
				case "转交":
					_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, nil, newTestTasks(f.db)).Execute(gated, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.OrganizationIdentity.ID})
				case "关闭":
					_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(gated, f.owner, f.conversationID)
				case "重开":
					_, err = servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(gated, f.owner, f.conversationID)
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
				if err := waitChatResult(t, ctx, done); err != nil {
					t.Fatal(err)
				}
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
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"contact_channel_identities"`)
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
	waitChatDatabaseLock(t, ctx, f.db, "last_contact_number", f.owner.Organization.ID)
	gate.open()
	for range 2 {
		if err := waitChatResult(t, ctx, done); err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Message.ID != results[1].Message.ID || results[0].Conversation.ID != results[1].Conversation.ID {
		t.Fatalf("results=%+v", results)
	}
	for _, table := range []string{"messages", "service_sessions", "service_conversations", "channel_conversations"} {
		count, err := f.db.NewSelect().TableExpr(table).Where("conversation_id = ?", results[0].Conversation.ID).Count(ctx)
		if err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
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
			closed, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID)
			if err != nil {
				t.Fatal(err)
			}
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
				return event.Operation() == "SELECT" && strings.Contains(event.Query, `FROM "conversations"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			reopened, received := make(chan error, 1), make(chan error, 1)
			reopen := func(c context.Context) {
				_, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(c, f.owner, f.conversationID)
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
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var conflict *conversationaction.ConflictError
				if !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonServiceSessionAlreadyOpen {
					t.Fatalf("reopen=%v", err)
				}
			}
			if err := waitChatResult(t, ctx, received); err != nil {
				t.Fatal(err)
			}
			var sessions []models.ServiceSession
			if err := f.db.NewSelect().Model(&sessions).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || (sessions[0].ID == closed.ID) != reopenFirst {
				t.Fatalf("sessions=%+v", sessions)
			}
			assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
		})
	}
}

// TestTelegramInboundCredentialLock 验证回调与停用操作的锁顺序及凭据有效性校验。
func TestTelegramInboundCredentialLock(t *testing.T) {
	t.Parallel()
	for _, disableFirst := range []bool{false, true} {
		name := "入站先提交"
		if disableFirst {
			name = "停用先提交"
		}
		t.Run(name, func(t *testing.T) {
			f := newCustomerDeliveryFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
				table := `FROM "conversations"`
				if disableFirst {
					table = `FROM "telegram_channel_settings"`
				}
				return event.Operation() == "SELECT" && strings.Contains(event.Query, table) && strings.Contains(event.Query, "FOR UPDATE")
			})
			receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
			received, disabled := make(chan error, 1), make(chan error, 1)
			receive := func(c context.Context) {
				received <- receiver.Execute(c, f.channelID, customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{SenderID: 12345, ChatID: 12345, MessageID: 2, Body: "等待中的入站", OriginatedAt: time.Now().UTC()}})
			}
			disable := func(c context.Context) {
				_, err := channelaction.NewUpdateTelegramChannelStatusAction(f.db, connectiontest.NewRunner(time.Second), &telegramBotAPIFake{}).Execute(c, f.owner, f.channelID, false)
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
			waitChatDatabaseLock(t, ctx, f.db, `FROM "telegram_channel_settings"`, f.channelID)
			gate.open()
			err := waitChatResult(t, ctx, received)
			if disableFirst {
				if !errors.Is(err, customerchataction.ErrTelegramWebhookUnauthorized) && !errors.Is(err, channelaction.ErrNotFound) {
					t.Fatalf("old callback=%v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := waitChatResult(t, ctx, disabled); err != nil {
				t.Fatal(err)
			}
			count, err := f.db.NewSelect().Model((*models.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
			want := 2
			if disableFirst {
				want = 1
			}
			if err != nil || count != want {
				t.Fatalf("messages=%d err=%v", count, err)
			}
			assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
		})
	}
}

// testCustomerSharedAgentSubject 验证同一 Agent 在不同客户会话首次回复时共用唯一聊天主体。
func testCustomerSharedAgentSubject(t *testing.T, db *bun.DB, identity *models.Identity, agentID string, tasks *servertask.Runtime) {
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
		model := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			claim, err := feed.Claim(ctx, 1)
			return agentruntime.RunResult{Content: "共同主体的回复", EndSeq: claim.EndSeq}, err
		}}
		executionCtx := ctx
		if i == 0 {
			executionCtx = context.WithValue(ctx, chatQueryGateKey{}, gate)
		}
		go func() {
			done <- agentrunaction.NewExecuteAction(db, tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(executionCtx, agentrunaction.RunInput{RunID: run.ID})
		}()
		if i == 0 {
			waitChatSignal(t, ctx, gate.reached)
		}
	}
	waitChatDatabaseLock(t, ctx, db, `INSERT INTO "chat_subjects"`, agentID)
	gate.open()
	for range 2 {
		if err := waitChatResult(t, ctx, done); err != nil {
			t.Fatal(err)
		}
	}
	count, err := db.NewSelect().Model((*models.ChatSubject)(nil)).Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id = ?", identity.Organization.ID, domain.ChatSubjectKindOrganizationIdentity, agentID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("subjects=%d err=%v", count, err)
	}
}
