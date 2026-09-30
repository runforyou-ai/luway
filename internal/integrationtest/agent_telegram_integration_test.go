//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	deliveryaction "github.com/runforyou-ai/cervi/internal/actions/customerdelivery"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

type agentTelegramFixture struct {
	db       *bun.DB
	identity *models.Identity
	channel  *channelaction.MessageChannelRecord
	tasks    *servertask.Runtime
	receiver *customerchataction.ReceiveTelegramWebhookAction
	input    customerchataction.TelegramWebhookInput
	run      models.AgentRun
}

// newAgentTelegramFixture 创建自动分配给 AI 客服的 Telegram 私聊。
func newAgentTelegramFixture(t *testing.T, db *bun.DB, identity *models.Identity, providerID, modelID string) agentTelegramFixture {
	t.Helper()
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "Telegram AI 客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "回答客户问题"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: "Telegram AI 验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_id = ?, bot_token = '123:token', webhook_secret = 'secret' WHERE channel_id = ?", time.Now().UnixNano(), channel.ID); err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(deliveryaction.SendActionName, func(context.Context, deliveryaction.Input) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := agentTelegramFixture{db: db, identity: identity, channel: channel, tasks: tasks, receiver: customerchataction.NewReceiveTelegramWebhookAction(db, agentrunaction.NewScheduler(tasks), domain.FileStorageBackendLocal, newTestTasks(db))}
	f.input = customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "请介绍产品", OriginatedAt: time.Now().UTC()}}
	if err := f.receiver.Execute(ctx, channel.ID, f.input); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&f.run).Where("agr.agent_identity_id = ?", agent.IdentityID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

// reload 读取运行的持久终态。
func (f *agentTelegramFixture) reload(t *testing.T) {
	t.Helper()
	if err := f.db.NewSelect().Model(&f.run).WherePK().Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// receiveNext 追加一条 Telegram 客户消息。
func (f *agentTelegramFixture) receiveNext(t *testing.T) {
	t.Helper()
	f.input.UpdateID++
	f.input.Message.MessageID++
	f.input.Message.Body = "补充：请说明使用方式"
	f.input.Message.OriginatedAt = time.Now().UTC()
	if err := f.receiver.Execute(context.Background(), f.channel.ID, f.input); err != nil {
		t.Fatal(err)
	}
}

// TestAgentTelegramReplies 验证自动接待、连续输入、事务投递及客服接管边界。
func TestAgentTelegramReplies(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	t.Run("引用上下文与历史窗口", func(t *testing.T) {
		for _, external := range []bool{false, true} {
			f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
			for range 100 {
				f.receiveNext(t)
			}
			target := int64(1)
			if external {
				target = 9999
			}
			f.input.Message.Reply = &telegram.InboundReply{MessageID: target, Body: "平台原文快照", SenderName: "外部机器人", SenderIsBot: true}
			f.receiveNext(t)
			model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				pending, err := feed.Peek(ctx, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				if len(claimed.Messages) != 100 {
					t.Fatalf("history=%d", len(claimed.Messages))
				}
				var payload struct {
					ReplyTo struct {
						MessageID   string `json:"messageId"`
						Body        string `json:"body"`
						External    bool   `json:"external"`
						SenderIsBot bool   `json:"senderIsBot"`
					} `json:"replyTo"`
				}
				last := claimed.Messages[len(claimed.Messages)-1]
				if err := json.Unmarshal([]byte(last.Content), &payload); err != nil {
					t.Fatal(err)
				}
				if last.Role != agentruntime.MessageRoleUser || payload.ReplyTo.External != external {
					t.Fatalf("reference=%+v", payload)
				}
				if external {
					if payload.ReplyTo.MessageID != "" || payload.ReplyTo.Body != "平台原文快照" || !payload.ReplyTo.SenderIsBot {
						t.Fatalf("snapshot=%+v", payload)
					}
				} else if payload.ReplyTo.MessageID == "" || payload.ReplyTo.Body != "请介绍产品" {
					t.Fatalf("reference=%+v", payload)
				}
				return agentruntime.RunResult{Content: "引用上下文验证完成", EndSeq: claimed.EndSeq}, nil
			}}
			if err := agentrunaction.NewExecuteAction(db, f.tasks, model, testAttachmentReader(db), nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: f.run.ID}); err != nil {
				t.Fatal(err)
			}
			f.reload(t)
			if f.run.Status != string(domain.AgentRunStatusSucceeded) {
				t.Fatalf("run=%+v", f.run)
			}
		}
	})

	t.Run("连续输入与幂等外发", func(t *testing.T) {
		f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
		ctx := context.Background()
		if err := f.receiver.Execute(ctx, f.channel.ID, f.input); err != nil {
			t.Fatal(err)
		}
		calls := 0
		model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			calls++
			pending, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if len(pending) != 1 {
				t.Fatalf("duplicate input: %+v", pending)
			}
			claimed, err := feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed.Messages = customerTurnMessages(t, claimed.Messages)
			if len(claimed.Messages) != 1 || claimed.Messages[0].Role != agentruntime.MessageRoleUser {
				t.Fatalf("context=%+v", claimed)
			}
			f.receiveNext(t)
			pending, err = feed.Peek(ctx, claimed.EndSeq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if len(pending) != 1 {
				t.Fatalf("pending=%+v", pending)
			}
			claimed, err = feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed.Messages = customerTurnMessages(t, claimed.Messages)
			if len(claimed.Messages) != 2 || claimed.Messages[1].Content != f.input.Message.Body {
				t.Fatalf("followup=%+v", claimed)
			}
			return agentruntime.RunResult{Content: "产品及使用方式", EndSeq: claimed.EndSeq}, nil
		}}
		executor := agentrunaction.NewExecuteAction(db, f.tasks, model, testAttachmentReader(db), nil, nil)
		for range 2 {
			if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: f.run.ID}); err != nil {
				t.Fatal(err)
			}
		}
		f.reload(t)
		if calls != 1 || f.run.Status != string(domain.AgentRunStatusSucceeded) || f.run.ResponseMessageID == nil || f.run.InputEndSeq == nil || *f.run.InputEndSeq != 2 {
			t.Fatalf("calls=%d run=%+v", calls, f.run)
		}
		var deliveries []models.CustomerMessageDelivery
		if err := db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", f.run.ConversationID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(deliveries) != 1 || deliveries[0].MessageID != *f.run.ResponseMessageID {
			t.Fatalf("deliveries=%+v", deliveries)
		}
		exists, err := db.NewSelect().TableExpr("task_runs AS tr").Join("JOIN task_outbox AS o ON o.task_run_id = tr.id").Where("tr.idempotency_key = ?", "cdeliv-item:"+deliveries[0].ID).Exists(ctx)
		if err != nil || !exists {
			t.Fatalf("delivery wakeup=%t err=%v", exists, err)
		}
		// 已提交的回复在人工接管后继续使用原投递记录。
		if _, err := servicesessionaction.NewClaimServiceSessionAction(db, executor, newTestTasks(db)).Execute(ctx, identity, f.run.ConversationID); err != nil {
			t.Fatal(err)
		}
		sender := &deliverySender{}
		worker := deliveryaction.NewWorker(db, sender, deliveryFiles{}, f.tasks)
		for range 2 {
			if err := worker.Execute(ctx, deliveryaction.Input{DeliveryID: deliveries[0].ID}); err != nil {
				t.Fatal(err)
			}
		}
		if len(sender.bodies) != 1 || sender.bodies[0] != "产品及使用方式" {
			t.Fatalf("sent=%v", sender.bodies)
		}
		// 人工接待时不触发，转回 AI 后补入最后一条客户消息。
		f.receiveNext(t)
		if n, err := db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ?", f.run.ConversationID).Count(ctx); err != nil || n != 1 {
			t.Fatalf("human runs=%d err=%v", n, err)
		}
		if _, err := servicesessionaction.NewTransferServiceSessionAction(db, executor, agentrunaction.NewScheduler(f.tasks), newTestTasks(db)).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: f.run.ConversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.run.AgentIdentityID}); err != nil {
			t.Fatal(err)
		}
		if n, err := db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", f.run.ConversationID, domain.AgentRunStatusQueued).Count(ctx); err != nil || n != 1 {
			t.Fatalf("transferred runs=%d err=%v", n, err)
		}
	})
	for _, scenario := range []string{"失败", "接管", "关闭", "更换机器人", "投递入队失败"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
			ctx := context.Background()
			coordinator := agentrunaction.NewExecuteAction(db, f.tasks, nil, testAttachmentReader(db), nil, nil)
			model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				pending, err := feed.Peek(ctx, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				switch scenario {
				case "失败":
					return agentruntime.RunResult{}, errors.New("模型失败详情")
				case "接管":
					_, err = servicesessionaction.NewClaimServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, f.run.ConversationID)
				case "关闭":
					if _, err = servicesessionaction.NewClaimServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, f.run.ConversationID); err == nil {
						_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, f.run.ConversationID)
					}
				case "更换机器人":
					api := &telegramBotAPIFake{bot: telegram.Bot{ID: time.Now().UnixNano(), IsBot: true, FirstName: "新机器人", Username: "new_bot"}}
					var updated *channelaction.TelegramChannelDetail
					updated, err = channelaction.NewSaveTelegramConnectionAction(db, connectiontest.NewRunner(time.Second), api).Execute(ctx, identity, f.channel.ID, channelaction.TelegramChannelConnectionInput{BotToken: "456:new_token", WebhookBaseURL: "https://example.com"})
					if err == nil {
						f.input.Secret = updated.Connection.WebhookSecret
					}
				}
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				return agentruntime.RunResult{Content: "不应外发的回答", EndSeq: claimed.EndSeq}, nil
			}}
			var enqueuer servertask.TxEnqueuer = f.tasks
			failing := &failingDeliveryEnqueuer{inner: f.tasks}
			if scenario == "投递入队失败" {
				enqueuer = failing
			}
			err := agentrunaction.NewExecuteAction(db, enqueuer, model, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: f.run.ID})
			if (scenario == "失败" || scenario == "投递入队失败") != (err != nil) {
				t.Fatalf("execution err=%v", err)
			}
			f.reload(t)
			// 失败转人工只投递一次对客通知，重复收尾保持一条；其余场景没有投递。
			if scenario == "失败" {
				if err := agentrunaction.NewExecuteAction(db, enqueuer, model, testAttachmentReader(db), nil, nil).FinalizeFailure(ctx, agentrunaction.RunInput{RunID: f.run.ID}, errors.New("重复收尾")); err != nil {
					t.Fatal(err)
				}
			}
			var deliveries []models.CustomerMessageDelivery
			if err := db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", f.run.ConversationID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if (scenario == "失败") != (len(deliveries) == 1) || len(deliveries) > 1 {
				t.Fatalf("deliveries=%+v", deliveries)
			}
			if scenario == "失败" {
				if f.run.Status != string(domain.AgentRunStatusFailed) || f.run.ResponseMessageID == nil || deliveries[0].MessageID != *f.run.ResponseMessageID ||
					f.run.Outcome == nil || *f.run.Outcome != string(domain.AgentRunOutcomeHandoff) ||
					f.run.OutcomeReason == nil || *f.run.OutcomeReason != string(domain.AgentHandoffReasonRuntimeFailed) {
					t.Fatalf("run=%+v", f.run)
				}
				var notice, failure models.Message
				if err := db.NewSelect().Model(&notice).Where("msg.id = ?", *f.run.ResponseMessageID).Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if err := db.NewSelect().Model(&failure).Where("msg.idempotency_key = ?", "agent:"+f.run.ID+":error").Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if notice.Type != string(domain.MessageTypeText) || notice.Body == "" ||
					failure.Type != string(domain.MessageTypeAgentError) || failure.Visibility != string(domain.MessageVisibilityInternal) {
					t.Fatalf("notice=%+v failure=%+v", notice, failure)
				}
			} else if scenario == "投递入队失败" {
				if !failing.observedAtomicRows || f.run.ResponseMessageID != nil || f.run.Status != string(domain.AgentRunStatusRunning) {
					t.Fatalf("atomic=%t run=%+v", failing.observedAtomicRows, f.run)
				}
				if n, err := db.NewSelect().Table("messages").Where("conversation_id = ? AND body = ?", f.run.ConversationID, "不应外发的回答").Count(ctx); err != nil || n != 0 {
					t.Fatalf("partial message=%d err=%v", n, err)
				}
			} else {
				if f.run.Status != string(domain.AgentRunStatusCancelled) || f.run.ResponseMessageID != nil {
					t.Fatalf("run=%+v", f.run)
				}
			}
			if scenario == "更换机器人" {
				if f.run.ErrorCode == nil || *f.run.ErrorCode != string(domain.AgentRunErrorCodeBotChanged) {
					t.Fatalf("run=%+v", f.run)
				}
				f.receiveNext(t)
				var state models.AgentLane
				if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", f.run.ConversationID).Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if state.ProcessedSeq != 1 || state.DesiredSeq != 2 {
					t.Fatalf("state=%+v", state)
				}
				if n, err := db.NewSelect().Table("agent_runs").Where("conversation_id = ? AND status = ?", f.run.ConversationID, domain.AgentRunStatusQueued).Count(ctx); err != nil || n != 1 {
					t.Fatalf("new bot runs=%d err=%v", n, err)
				}
			}
		})
	}
}
