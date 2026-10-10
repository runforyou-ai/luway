//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// agentTelegramFixture 是 AI 员工接待 Telegram 消息的测试环境。
type agentTelegramFixture struct {
	db       *bun.DB
	identity *models.Identity
	channel  *channelaction.MessageChannelRecord
	tasks    *servertest.Tasks
	receiver *telegramWebhook
	input    telegramUpdate
	run      models.AgentRun
}

// newAgentTelegramFixture 创建自动分配给 AI 客服的 Telegram 私聊。
func newAgentTelegramFixture(t *testing.T, db *bun.DB, identity *models.Identity, providerID, modelID string) agentTelegramFixture {
	t.Helper()
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "Telegram AI 客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答客户问题"}},
	})
	require.NoError(t, err)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: "Telegram AI 验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	require.NoError(t, connectTestTelegramBot(ctx, db, channel.ID, time.Now().UnixNano(), "123:token"))
	tasks := servertest.NewTasks()
	f := agentTelegramFixture{db: db, identity: identity, channel: channel, tasks: tasks, receiver: newTelegramWebhook(db, agentrunaction.NewScheduler(tasks), testEnqueuer)}
	f.input = telegramUpdate{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "请介绍产品", OriginatedAt: time.Now().UTC()}}
	require.NoError(t, f.receiver.Execute(ctx, channel.ID, f.input))
	require.NoError(t, db.NewSelect().Model(&f.run).Where("agr.agent_identity_id = ?", agent.IdentityID).Scan(ctx))
	return f
}

// reload 读取运行的持久终态。
func (f *agentTelegramFixture) reload(t *testing.T) {
	t.Helper()
	require.NoError(t, f.db.NewSelect().Model(&f.run).WherePK().Scan(context.Background()))
}

// receiveNext 追加一条 Telegram 客户消息。
func (f *agentTelegramFixture) receiveNext(t *testing.T) {
	t.Helper()
	f.input.UpdateID++
	f.input.Message.MessageID++
	f.input.Message.Body = "补充：请说明使用方式"
	f.input.Message.OriginatedAt = time.Now().UTC()
	require.NoError(t, f.receiver.Execute(context.Background(), f.channel.ID, f.input))
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
			model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				pending, err := pendingTriggers(ctx, feed, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				require.Len(t, claimed.Messages, 100, "history")
				var payload struct {
					ReplyTo struct {
						MessageID   string `json:"messageId"`
						Body        string `json:"body"`
						External    bool   `json:"external"`
						SenderIsBot bool   `json:"senderIsBot"`
					} `json:"replyTo"`
				}
				last := claimed.Messages[len(claimed.Messages)-1]
				require.NoError(t, json.Unmarshal([]byte(last.Content), &payload))
				require.Equal(t, einorun.RoleUser, last.Role)
				require.Equal(t, external, payload.ReplyTo.External)
				if external {
					require.Empty(t, payload.ReplyTo.MessageID, "snapshot=%+v", payload)
					require.Equal(t, "平台原文快照", payload.ReplyTo.Body)
					require.True(t, payload.ReplyTo.SenderIsBot, "snapshot=%+v", payload)
				} else {
					require.NotEmpty(t, payload.ReplyTo.MessageID, "reference=%+v", payload)
					require.Equal(t, "请介绍产品", payload.ReplyTo.Body)
				}
				return agentruntime.RunResult{Content: "引用上下文验证完成", EndSeq: claimed.EndSeq}, nil
			}}
			require.NoError(t, newTestAgentRun(db, f.tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: f.run.ID}))
			f.reload(t)
			require.Equal(t, string(domain.AgentRunStatusSucceeded), f.run.Status, "run=%+v", f.run)
		}
	})

	t.Run("连续输入与幂等外发", func(t *testing.T) {
		f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
		ctx := context.Background()
		require.NoError(t, f.receiver.Execute(ctx, f.channel.ID, f.input))
		calls := 0
		model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			calls++
			pending, err := pendingTriggers(ctx, feed, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			require.Len(t, pending, 1, "duplicate input")
			claimed, err := feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed.Messages = customerTurnMessages(t, claimed.Messages)
			require.Len(t, claimed.Messages, 1, "context")
			require.Equal(t, einorun.RoleUser, claimed.Messages[0].Role)
			f.receiveNext(t)
			pending, err = pendingTriggers(ctx, feed, claimed.EndSeq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			require.Len(t, pending, 1, "pending")
			claimed, err = feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed.Messages = customerTurnMessages(t, claimed.Messages)
			require.Len(t, claimed.Messages, 2, "followup")
			require.Equal(t, f.input.Message.Body, claimed.Messages[1].Content, "followup")
			return agentruntime.RunResult{Content: "产品及使用方式", EndSeq: claimed.EndSeq}, nil
		}}
		executor := newTestAgentRun(db, f.tasks, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
		for range 2 {
			require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: f.run.ID}))
		}
		f.reload(t)
		require.Equal(t, 1, calls, "calls")
		require.Equal(t, string(domain.AgentRunStatusSucceeded), f.run.Status)
		require.NotNil(t, f.run.ResponseMessageID)
		require.NotNil(t, f.run.InputEndSeq)
		require.Equal(t, int64(2), *f.run.InputEndSeq)
		var deliveries []models.ChannelMessageDelivery
		require.NoError(t, db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", f.run.ConversationID).Scan(ctx))
		require.Len(t, deliveries, 1)
		require.Equal(t, *f.run.ResponseMessageID, deliveries[0].MessageID)
		wakeups := servertest.QueuedInputs(t, f.tasks, deliveryaction.AdvanceActionName, func(input deliveryaction.AdvanceInput) bool {
			return input.ChannelIdentityID == deliveries[0].ChannelIdentityID
		})
		require.NotEmpty(t, wakeups, "delivery wakeup")
		// 已提交的回复在人工接管后继续使用原投递记录。
		_, err := servicesessionaction.NewClaimServiceSessionAction(db, executor, testEnqueuer).Execute(ctx, identity, f.run.ConversationID)
		require.NoError(t, err)
		sender := &deliverySender{}
		worker := deliveryaction.NewWorker(db, telegramAdapters(db, sender, nil), deliveryFiles{}, f.tasks)
		for range 2 {
			advanceDelivery(t, db, worker, deliveries[0].ID)
		}
		require.Equal(t, []string{"产品及使用方式"}, sender.bodies)
		// 人工接待时不触发，转回 AI 后补入最后一条客户消息。
		f.receiveNext(t)
		n, err := db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ?", f.run.ConversationID).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), n, "human runs")
		_, err = servicesessionaction.NewTransferServiceSessionAction(db, executor, agentrunaction.NewScheduler(f.tasks), testEnqueuer).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{ConversationID: f.run.ConversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.run.AgentIdentityID})
		require.NoError(t, err)
		n, err = db.NewSelect().Model((*models.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", f.run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), n, "transferred runs")
	})
	for _, scenario := range []string{"失败", "接管", "关闭", "更换机器人", "投递入队失败"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
			ctx := context.Background()
			coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
			model := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				pending, err := pendingTriggers(ctx, feed, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, pending[len(pending)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed.Messages = customerTurnMessages(t, claimed.Messages)
				// 接管、关闭与更换机器人在独立请求中执行，不随本次被取消的运行中断。
				request := context.WithoutCancel(ctx)
				switch scenario {
				case "失败":
					return agentruntime.RunResult{}, errors.New("模型失败详情")
				case "接管":
					_, err = servicesessionaction.NewClaimServiceSessionAction(db, coordinator, testEnqueuer).Execute(request, identity, f.run.ConversationID)
				case "关闭":
					if _, err = servicesessionaction.NewClaimServiceSessionAction(db, coordinator, testEnqueuer).Execute(request, identity, f.run.ConversationID); err == nil {
						_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer).Execute(request, identity, f.run.ConversationID)
					}
				case "更换机器人":
					api := &telegramBotAPIFake{bot: telegram.Bot{ID: time.Now().UnixNano(), IsBot: true, FirstName: "新机器人", Username: "new_bot"}}
					var updated *telegramaction.ChannelDetail
					updated, err = telegramaction.NewSaveConnectionAction(db, connectiontest.NewRunner(time.Second), api).Execute(request, identity, f.channel.ID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect, BotToken: "456:new_token", WebhookBaseURL: "https://example.com"})
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
			err := newTestAgentRun(db, enqueuer, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: f.run.ID})
			require.Equal(t, scenario == "失败" || scenario == "投递入队失败", err != nil, "execution err=%v", err)
			f.reload(t)
			// 失败转人工只投递一次对客通知，重复收尾保持一条；其余场景没有投递。
			if scenario == "失败" {
				require.NoError(t, newTestAgentRun(db, enqueuer, model, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).FinalizeFailure(ctx, agentrunaction.RunInput{RunID: f.run.ID}, errors.New("重复收尾")))
			}
			var deliveries []models.ChannelMessageDelivery
			require.NoError(t, db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", f.run.ConversationID).Scan(ctx))
			require.Equal(t, scenario == "失败", len(deliveries) == 1, "deliveries=%+v", deliveries)
			require.LessOrEqual(t, len(deliveries), 1, "deliveries=%+v", deliveries)
			switch scenario {
			case "失败":
				require.Equal(t, string(domain.AgentRunStatusFailed), f.run.Status)
				require.NotNil(t, f.run.ResponseMessageID)
				require.Equal(t, *f.run.ResponseMessageID, deliveries[0].MessageID)
				require.NotNil(t, f.run.Outcome)
				require.Equal(t, string(domain.AgentRunOutcomeHandoff), *f.run.Outcome)
				require.NotNil(t, f.run.OutcomeReason)
				require.Equal(t, string(domain.AgentHandoffReasonRuntimeFailed), *f.run.OutcomeReason)
				var notice, failure models.Message
				require.NoError(t, db.NewSelect().Model(&notice).Where("msg.id = ?", *f.run.ResponseMessageID).Scan(ctx))
				require.NoError(t, db.NewSelect().Model(&failure).Where("msg.idempotency_key = ?", "agent:"+f.run.ID+":error").Scan(ctx))
				require.Equal(t, string(domain.MessageTypeText), notice.Type)
				require.NotEmpty(t, notice.Body)
				require.Equal(t, string(domain.MessageTypeAgentError), failure.Type)
				require.Equal(t, string(domain.MessageVisibilityInternal), failure.Visibility)
			case "投递入队失败":
				require.NotEmpty(t, failing.identityID, "delivery wakeup was not enqueued")
				require.Nil(t, f.run.ResponseMessageID)
				require.Equal(t, string(domain.AgentRunStatusRunning), f.run.Status)
				n, err := db.NewSelect().Table("messages").Where("conversation_id = ? AND body = ?", f.run.ConversationID, "不应外发的回答").Count(ctx)
				require.NoError(t, err)
				require.Zero(t, n, "partial message")
			default:
				require.Equal(t, string(domain.AgentRunStatusCancelled), f.run.Status)
				require.Nil(t, f.run.ResponseMessageID)
			}
			if scenario == "更换机器人" {
				require.NotNil(t, f.run.ErrorCode)
				require.Equal(t, string(domain.AgentRunErrorCodeAccountChanged), *f.run.ErrorCode)
				f.receiveNext(t)
				var state models.AgentLane
				require.NoError(t, db.NewSelect().Model(&state).Where("al.conversation_id = ?", f.run.ConversationID).Scan(ctx))
				require.Equal(t, int64(1), state.ProcessedSeq)
				require.Equal(t, int64(2), state.DesiredSeq)
				n, err := db.NewSelect().Table("agent_runs").Where("conversation_id = ? AND status = ?", f.run.ConversationID, domain.AgentRunStatusQueued).Count(ctx)
				require.NoError(t, err)
				require.Equal(t, int64(1), n, "new bot runs")
			}
		})
	}
}
