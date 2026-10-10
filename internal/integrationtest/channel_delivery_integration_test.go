//go:build server

package integrationtest

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// channelDeliveryFixture 是渠道消息投递的测试环境。
type channelDeliveryFixture struct {
	customerReadFixture
	sender *deliverySender
	worker *deliveryaction.Worker
	files  deliveryFiles
}

// deliverySender 记录外发内容的渠道发送替身。
type deliverySender struct {
	mu      sync.Mutex
	bodies  []string
	replies []*string
	media   []sentMedia
	err     error
	// onMedia 在媒体发送进行中回调，供测试观察认领状态。
	onMedia func()
	// onText 在文本发送进行中回调，供测试控制发送完成的时机。
	onText func()
}

// sentMedia 记录一次媒体投递的文件元数据与实际读取到的内容。
type sentMedia struct {
	name, contentType, content string
	width, height              int
}

// deliveryFiles 按存储键返回测试附件内容，未登记的键视为存储读取失败。
type deliveryFiles map[string]string

// Open 返回登记的附件内容，失败时与本地存储读取器一样携带空的具体类型值。
func (f deliveryFiles) Open(_ context.Context, file *models.File) (io.ReadCloser, error) {
	content, ok := f[file.StorageKey]
	if !ok {
		var missing *os.File
		return missing, errors.New("object missing")
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

// SendText 记录平台调用并返回可控制的发送结果。
func (s *deliverySender) SendText(_ context.Context, _ string, message telegram.TextMessage) (int64, error) {
	if s.onText != nil {
		s.onText()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, message.Body)
	s.replies = append(s.replies, message.ReplyMessageID)
	if message.ChatID != "12345" && message.ChatID != "67890" {
		return 0, errors.New("unexpected recipient")
	}
	return int64(1000 + len(s.bodies)), s.err
}

// SendMedia 记录媒体调用，说明与引用和文本共用记录序列。
func (s *deliverySender) SendMedia(_ context.Context, _ string, message telegram.MediaMessage) (int64, error) {
	if s.onMedia != nil {
		s.onMedia()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := io.ReadAll(message.Content)
	if err != nil {
		return 0, err
	}
	s.bodies = append(s.bodies, message.Caption)
	s.replies = append(s.replies, message.ReplyMessageID)
	s.media = append(s.media, sentMedia{message.FileName, message.ContentType, string(content), message.ImageWidth, message.ImageHeight})
	if message.ChatID != "12345" {
		return 0, errors.New("unexpected recipient")
	}
	return int64(1000 + len(s.bodies)), s.err
}

// connectTestTelegramBot 把 Telegram 渠道连接到指定机器人编号与凭据，回调 Secret 固定为 secret。
func connectTestTelegramBot(ctx context.Context, db bun.IDB, channelID string, botID int64, token string) error {
	if _, err := db.ExecContext(ctx, "UPDATE channels SET provider_account_id = ? WHERE id = ?", strconv.FormatInt(botID, 10), channelID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_token = ?, webhook_secret = 'secret' WHERE channel_id = ?", token, channelID)
	return err
}

// newChannelDeliveryFixture 建立已配置的 Telegram 私聊和两个客服身份。
func newChannelDeliveryFixture(t *testing.T) channelDeliveryFixture {
	t.Helper()
	f := newCustomerReadFixture(t)
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), "DELETE FROM channel_message_deliveries WHERE workspace_id = ?", f.owner.Workspace.ID)
		_, _ = f.db.ExecContext(context.Background(), "DELETE FROM channel_send_gates WHERE workspace_id = ?", f.owner.Workspace.ID)
	})
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: "Telegram 投递测试", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	f.channelID = channel.ID
	require.NoError(t, connectTestTelegramBot(ctx, f.db, channel.ID, 123, "123:token"))
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(ctx, channel.ID, telegramUpdate{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "你好", OriginatedAt: time.Now().UTC()}}))
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cc.conversation_id").Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").Where("ci.channel_id = ?", channel.ID).Scan(ctx, &f.conversationID))
	sender := &deliverySender{}
	files := deliveryFiles{}
	worker := deliveryaction.NewWorker(f.db, telegramAdapters(f.db, sender, nil), files, testEnqueuer)
	return channelDeliveryFixture{f, sender, worker, files}
}

// send 保存一条客服消息并读取对应投递。
func (f channelDeliveryFixture) send(t *testing.T, body, clientID string) models.ChannelMessageDelivery {
	t.Helper()
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(context.Background(), f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: clientID, Body: body})
	require.NoError(t, err)
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("message_id = ?", message.ID).Scan(context.Background()))
	return delivery
}

// load 读取投递的持久状态。
func (f channelDeliveryFixture) load(t *testing.T, id string) models.ChannelMessageDelivery {
	t.Helper()
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("id = ?", id).Scan(context.Background()))
	return delivery
}

// providerMessageID 读取投递首个请求项的平台消息编号，平台未返回或尚未发送时为空字符串。
func (f channelDeliveryFixture) providerMessageID(t *testing.T, id string) string {
	t.Helper()
	var providerID *string
	if err := f.db.NewSelect().TableExpr("channel_delivery_items").Column("provider_message_id").Where("delivery_id = ? AND seq = 1", id).Scan(context.Background(), &providerID); !errors.Is(err, sql.ErrNoRows) {
		require.NoError(t, err)
	}
	if providerID == nil {
		return ""
	}
	return *providerID
}

// execute 推进一次投递所属渠道身份的管道并重新读取该投递。
func (f channelDeliveryFixture) execute(t *testing.T, id string) models.ChannelMessageDelivery {
	t.Helper()
	advanceDelivery(t, f.db, f.worker, id)
	return f.load(t, id)
}

// advanceDelivery 推进一次投递所属渠道身份的管道。
func advanceDelivery(t *testing.T, db bun.IDB, worker *deliveryaction.Worker, id string) {
	t.Helper()
	ctx := context.Background()
	var input deliveryaction.AdvanceInput
	require.NoError(t, db.NewSelect().TableExpr("channel_message_deliveries").Column("workspace_id", "channel_identity_id").Where("id = ?", id).Scan(ctx, &input.WorkspaceID, &input.ChannelIdentityID))
	require.NoError(t, worker.Advance(ctx, input))
}

// advances 返回渠道身份已排队的推进任务数，scheduled 为真时只计定时推进，否则只计立即推进。
func (f channelDeliveryFixture) advances(t *testing.T, identityID string, scheduled bool) int {
	t.Helper()
	count := 0
	for _, task := range testEnqueuer.Queued(deliveryaction.AdvanceActionName, "") {
		if servertest.TaskPayload[deliveryaction.AdvanceInput](t, task).ChannelIdentityID == identityID && (task.Options.IdempotencyKey != "") == scheduled {
			count++
		}
	}
	return count
}

// requireWakeAt 断言渠道身份已在 at 排了一次推进。
func (f channelDeliveryFixture) requireWakeAt(t *testing.T, identityID string, at time.Time) {
	t.Helper()
	// 定时推进的去重键带有推进时刻。
	keys := make([]string, 0)
	for _, task := range testEnqueuer.Queued(deliveryaction.AdvanceActionName, "") {
		if servertest.TaskPayload[deliveryaction.AdvanceInput](t, task).ChannelIdentityID == identityID && task.Options.IdempotencyKey != "" {
			if task.Options.IdempotencyKey == "chdeliv:"+identityID+":"+at.UTC().Format(time.RFC3339Nano) {
				return
			}
			keys = append(keys, task.Options.IdempotencyKey)
		}
	}
	t.Fatalf("no advance scheduled at %s, scheduled=%v", at, keys)
}

// TestChannelDeliveryFIFO 验证幂等入队、队头发送进行中时的推进与身份顺序。
func TestChannelDeliveryFIFO(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	clientID := uuid.NewV7().String()
	first := f.send(t, "第一条", clientID)
	replay := f.send(t, "第一条", clientID)
	require.Equal(t, first.ID, replay.ID, "duplicate delivery")
	second := f.send(t, "第二条", uuid.NewV7().String())
	// 第一条发送进行中时再次推进同一渠道身份，不认领后续投递。
	release := make(chan struct{})
	f.sender.onText = func() { <-release }
	var input deliveryaction.AdvanceInput
	require.NoError(t, f.db.NewSelect().TableExpr("channel_message_deliveries").Column("workspace_id", "channel_identity_id").Where("id = ?", first.ID).Scan(context.Background(), &input.WorkspaceID, &input.ChannelIdentityID))
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, f.worker.Advance(context.Background(), input))
	}()
	require.Eventually(t, func() bool {
		var status domain.ChannelDeliveryStatus
		err := f.db.NewSelect().TableExpr("channel_message_deliveries").Column("status").Where("id = ?", first.ID).Scan(context.Background(), &status)
		return assert.NoError(t, err) && status == domain.ChannelDeliverySending
	}, 5*time.Second, 20*time.Millisecond)
	advanceDelivery(t, f.db, f.worker, second.ID)
	close(release)
	<-done
	f.sender.onText = nil
	require.Equal(t, []string{"第一条"}, f.sender.bodies, "queue skipped head")
	require.Equal(t, domain.ChannelDeliveryPending, f.load(t, second.ID).Status)
	got := f.execute(t, second.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status)
	require.NotEmpty(t, f.providerMessageID(t, second.ID))
	require.Equal(t, []string{"第一条", "第二条"}, f.sender.bodies)
}

// TestChannelDeliveryIdentitiesSendInParallel 验证同一渠道的一位客户的发送进行中时，其他客户的投递照常发送。
func TestChannelDeliveryIdentitiesSendInParallel(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(ctx, f.channelID, telegramUpdate{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{ChatID: 67890, SenderID: 67890, MessageID: 1, DisplayName: "另一位客户", Body: "在吗", OriginatedAt: time.Now().UTC()}}))
	other := f
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cc.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Where("ci.channel_id = ? AND ci.external_id = ?", f.channelID, "67890").Scan(ctx, &other.conversationID))
	media := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, "视频.mp4", "video/mp4")}, "mp4")
	text := other.send(t, "另一位客户的回复", uuid.NewV7().String())
	var parallel models.ChannelMessageDelivery
	f.sender.onMedia = func() {
		parallel = f.execute(t, text.ID)
	}
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, media.ID).Status)
	require.Equal(t, domain.ChannelDeliverySent, parallel.Status)
}

// TestTelegramFirstInboundConcurrent 验证新客户的多条首批消息并发到达时全部入站并归入同一渠道身份。
func TestTelegramFirstInboundConcurrent(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	for customer := range int64(5) {
		chatID := 70000 + customer
		var wg sync.WaitGroup
		errs := make(chan error, 3)
		for message := range int64(3) {
			wg.Go(func() {
				errs <- receiver.Execute(ctx, f.channelID, telegramUpdate{Secret: "secret", UpdateID: chatID*10 + message, Message: &telegram.InboundMessage{
					ChatID: chatID, SenderID: chatID, MessageID: message + 1, DisplayName: "并发客户", Body: "你好", OriginatedAt: time.Now().UTC(),
				}})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		count, err := f.db.NewSelect().TableExpr("messages AS m").
			Join("JOIN channel_conversations AS cc ON cc.conversation_id = m.conversation_id").
			Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
			Where("ci.channel_id = ? AND ci.external_id = ? AND m.type = ?", f.channelID, strconv.FormatInt(chatID, 10), domain.MessageTypeText).Count(ctx)
		require.NoError(t, err, "customer %d", chatID)
		require.Equal(t, int64(3), count, "customer %d", chatID)
	}
}

// TestChannelDeliveryAdvanceContinues 验证完成发送后排一次立即推进，队头在认领前失败时同一次推进继续发送下一个队头。
func TestChannelDeliveryAdvanceContinues(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "第一条", uuid.NewV7().String())
	second := f.send(t, "第二条", uuid.NewV7().String())
	queued := f.advances(t, first.ChannelIdentityID, false)
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, first.ID).Status)
	require.Equal(t, queued+1, f.advances(t, first.ChannelIdentityID, false), "next head wakeup")
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET provider_account_id = '456' WHERE id = ?", second.ID)
	require.NoError(t, err)
	third := f.send(t, "第三条", uuid.NewV7().String())
	failed := f.execute(t, second.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, failed.Status)
	require.Equal(t, "account_changed", failed.LastError)
	require.Equal(t, domain.ChannelDeliverySent, f.load(t, third.ID).Status)
	require.Equal(t, []string{"第一条", "第三条"}, f.sender.bodies)
	// 没有未完成投递时不再排推进。
	queued = f.advances(t, first.ChannelIdentityID, false)
	f.execute(t, third.ID)
	require.Equal(t, queued, f.advances(t, first.ChannelIdentityID, false), "idle wakeup")
}

// TestChannelDeliveryUnknownRecovery 验证未知结果阻塞、人工重试排队与风险确认。
func TestChannelDeliveryUnknownRecovery(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "结果未知", uuid.NewV7().String())
	second := f.send(t, "后续消息", uuid.NewV7().String())
	f.sender.err = &telegram.SendError{Code: "unknown_result"}
	uncertain := f.execute(t, first.ID)
	require.Equal(t, domain.ChannelDeliveryUncertain, uncertain.Status)
	f.execute(t, first.ID)
	f.requireWakeAt(t, first.ChannelIdentityID, *uncertain.UncertainUntil)
	require.Len(t, f.sender.bodies, 1, "unknown result automatically resent")
	// 待确认到期转入人工确认后，同一次推进继续发送下一个队头。
	f.sender.err = nil
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET uncertain_until = now() - interval '1 second' WHERE id = ?", first.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ChannelDeliveryNeedsReview, f.execute(t, first.ID).Status)
	require.Equal(t, domain.ChannelDeliverySent, f.load(t, second.ID).Status)
	require.Equal(t, "后续消息", f.sender.bodies[1])
	manager := deliveryaction.NewManager(f.db, testEnqueuer)
	require.ErrorIs(t, manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.ChannelDeliveryRetry, false), deliveryaction.ErrConflict, "risk confirmation")
	queued := f.advances(t, first.ChannelIdentityID, false)
	require.NoError(t, manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.ChannelDeliveryRetry, true))
	require.Equal(t, queued+1, f.advances(t, first.ChannelIdentityID, false), "retry wakeup")
	require.Greater(t, f.load(t, first.ID).Position, second.Position, "retry not at tail")
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, first.ID).Status)
	require.Equal(t, []string{"结果未知", "后续消息", "结果未知"}, f.sender.bodies)
}

// TestChannelDeliveryLeaseAndLifecycle 验证过期认领、渠道停用和机器人变化。
func TestChannelDeliveryLeaseAndLifecycle(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "中断消息", uuid.NewV7().String())
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'sending', lease_worker = ?, lease_expires_at = now() - interval '1 second' WHERE id = ?", uuid.NewV7().String(), first.ID)
	require.NoError(t, err)
	uncertain := f.execute(t, first.ID)
	require.Equal(t, domain.ChannelDeliveryUncertain, uncertain.Status, "expired sending was resent")
	f.requireWakeAt(t, first.ChannelIdentityID, *uncertain.UncertainUntil)
	require.Empty(t, f.sender.bodies, "expired sending was resent")
	_, err = f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET uncertain_until = now() - interval '1 second' WHERE id = ?", first.ID)
	require.NoError(t, err)
	f.execute(t, first.ID)
	// 渠道停用时投递暂停且不排推进，启用渠道时排一次立即推进。
	second := f.send(t, "等待恢复", uuid.NewV7().String())
	status := newTestChannelStatusAction(f.db)
	_, err = status.Execute(ctx, f.owner, f.channelID, false)
	require.NoError(t, err)
	queued, scheduled := f.advances(t, first.ChannelIdentityID, false), f.advances(t, first.ChannelIdentityID, true)
	paused := f.execute(t, second.ID)
	require.Equal(t, domain.ChannelDeliveryPending, paused.Status)
	require.Empty(t, paused.LastError)
	require.Equal(t, scheduled, f.advances(t, first.ChannelIdentityID, true), "paused delivery scheduled")
	_, err = status.Execute(ctx, f.owner, f.channelID, true)
	require.NoError(t, err)
	require.Equal(t, queued+1, f.advances(t, first.ChannelIdentityID, false), "enable wakeup")
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, second.ID).Status)
	third := f.send(t, "旧机器人消息", uuid.NewV7().String())
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET provider_account_id = '456' WHERE id = ?", f.channelID)
	require.NoError(t, err)
	changed := f.execute(t, third.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, changed.Status)
	require.Equal(t, "account_changed", changed.LastError)
	require.Len(t, f.sender.bodies, 1)
}

// TestChannelDeliveryRateLimitAndIsolation 验证渠道等待、永久拒绝和企业隔离。
func TestChannelDeliveryRateLimitAndIsolation(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "限流消息", uuid.NewV7().String())
	f.sender.err = &telegram.SendError{Code: "rate_limited", RetryAfter: time.Minute}
	waiting := f.execute(t, first.ID)
	require.Equal(t, domain.ChannelDeliveryRetryWait, waiting.Status)
	// 重复推进按同一到期时刻只排一次。
	f.execute(t, first.ID)
	scheduled := f.advances(t, first.ChannelIdentityID, true)
	f.execute(t, first.ID)
	f.requireWakeAt(t, first.ChannelIdentityID, waiting.AvailableAt)
	require.Equal(t, scheduled, f.advances(t, first.ChannelIdentityID, true), "duplicate scheduled advance")
	require.Len(t, f.sender.bodies, 1, "ignored rate limit")
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET available_at = now() - interval '1 second' WHERE id = ?", first.ID)
	require.NoError(t, err)
	f.execute(t, first.ID)
	require.Len(t, f.sender.bodies, 1, "ignored channel gate")
	var gate time.Time
	require.NoError(t, f.db.NewSelect().TableExpr("channel_send_gates").Column("flood_wait_until").Where("channel_id = ?", f.channelID).Scan(ctx, &gate))
	f.requireWakeAt(t, first.ChannelIdentityID, gate)
	_, err = f.db.ExecContext(ctx, "UPDATE channel_send_gates SET flood_wait_until = now() - interval '1 second' WHERE channel_id = ?", f.channelID)
	require.NoError(t, err)
	// 定时推进遇到可发送的队头不调用平台，排一次立即推进。
	queued := f.advances(t, first.ChannelIdentityID, false)
	require.NoError(t, f.worker.Advance(ctx, deliveryaction.AdvanceInput{WorkspaceID: first.WorkspaceID, ChannelIdentityID: first.ChannelIdentityID, Scheduled: true}))
	require.Len(t, f.sender.bodies, 1, "scheduled advance called Telegram")
	require.Equal(t, queued+1, f.advances(t, first.ChannelIdentityID, false), "scheduled advance handoff")
	f.sender.err = &telegram.SendError{Code: "recipient_unavailable"}
	require.Equal(t, domain.ChannelDeliveryFailed, f.execute(t, first.ID).Status)
	rows, err := deliveryaction.ListForMessages(ctx, f.db, uuid.NewV7().String(), f.conversationID, []string{first.MessageID})
	require.NoError(t, err)
	require.Empty(t, rows, "cross-workspace delivery visible")
	manager := deliveryaction.NewManager(f.db, testEnqueuer)
	f.sender.err = nil
	require.NoError(t, manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.ChannelDeliveryRetry, false))
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, first.ID).Status)
}

// TestChannelDeliveryDisabledRecoveryAndManualConfirmation 验证渠道停用时推进仍处理过期认领与待确认，以及人工确认。
func TestChannelDeliveryDisabledRecoveryAndManualConfirmation(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	// 渠道停用时推进仍把认领过期的投递转入待确认，待确认到期后转入人工确认，不调用平台。
	interrupted := f.send(t, "中断后停用", uuid.NewV7().String())
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'sending', lease_worker = ?, lease_expires_at = now() - interval '1 second' WHERE id = ?", uuid.NewV7().String(), interrupted.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET enabled = false WHERE id = ?", f.channelID)
	require.NoError(t, err)
	require.Equal(t, domain.ChannelDeliveryUncertain, f.execute(t, interrupted.ID).Status, "expired sending")
	_, err = f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET uncertain_until = now() - interval '1 second' WHERE id = ?", interrupted.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ChannelDeliveryNeedsReview, f.execute(t, interrupted.ID).Status, "expired uncertain")
	require.Empty(t, f.sender.bodies, "recovery called Telegram")
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channelID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'failed' WHERE id = ?", interrupted.ID)
	require.NoError(t, err)
	second := f.send(t, "人工确认", uuid.NewV7().String())
	_, err = f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'needs_review', last_error = 'unknown_result' WHERE id = ?", second.ID)
	require.NoError(t, err)
	manager := deliveryaction.NewManager(f.db, nil)
	require.NoError(t, manager.Resolve(ctx, f.owner, f.conversationID, second.ID, domain.ChannelDeliveryConfirmSent, false))
	got := f.load(t, second.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status)
	require.Empty(t, f.providerMessageID(t, got.ID))
	require.Equal(t, "manually_confirmed", got.LastError)
	require.ErrorIs(t, manager.Resolve(ctx, f.owner, f.conversationID, second.ID, domain.ChannelDeliveryRetry, true), deliveryaction.ErrConflict, "stale operation accepted")
	other := newChannelDeliveryFixture(t)
	require.ErrorIs(t, manager.Resolve(ctx, other.owner, f.conversationID, second.ID, domain.ChannelDeliveryConfirmFailed, false), deliveryaction.ErrUnavailable, "cross-workspace operation accepted")
}

// failingDeliveryEnqueuer 在投递唤醒登记后模拟失败的任务投递器。
type failingDeliveryEnqueuer struct {
	inner      servertask.TxEnqueuer
	identityID string
	taskID     string
}

// EnqueueIn 对投递唤醒先在事务内登记任务，再模拟投递失败；其他任务转交内部登记器。
func (e *failingDeliveryEnqueuer) EnqueueIn(ctx context.Context, action string, input any, options servertask.EnqueueOptions) (string, error) {
	if action != deliveryaction.AdvanceActionName {
		return e.inner.EnqueueIn(ctx, action, input, options)
	}
	e.identityID = input.(deliveryaction.AdvanceInput).ChannelIdentityID
	var err error
	e.taskID, err = e.inner.EnqueueIn(ctx, action, input, options)
	if err != nil {
		return "", err
	}
	return "", errors.New("enqueue failed")
}

// EnqueueManyIn 转交内部登记器。
func (e *failingDeliveryEnqueuer) EnqueueManyIn(ctx context.Context, requests []servertask.EnqueueRequest) error {
	return e.inner.EnqueueManyIn(ctx, requests)
}

// TestChannelDeliveryAtomicEnqueue 验证唤醒失败回滚消息与投递，成功时提交可靠任务。
func TestChannelDeliveryAtomicEnqueue(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "必须原子提交"}
	runtime := servertest.NewTasks()
	before := &models.Conversation{ID: f.conversationID}
	require.NoError(t, f.db.NewSelect().Model(before).WherePK().Scan(ctx))
	failing := &failingDeliveryEnqueuer{inner: runtime}
	_, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, failing).Execute(ctx, f.owner, input)
	require.Error(t, err)
	require.NotEmpty(t, failing.identityID, "delivery wakeup was not enqueued")
	exists, err := f.db.NewSelect().TableExpr("channel_message_deliveries").Where("conversation_id = ?", f.conversationID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "delivery survived rollback")
	exists, err = f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND body = ?", f.conversationID, input.Body).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "message survived rollback")
	require.NotEmpty(t, failing.taskID, "task was not written before rollback")
	require.Empty(t, runtime.Queued(deliveryaction.AdvanceActionName, ""), "delivery wakeup survived rollback")
	after := &models.Conversation{ID: f.conversationID}
	require.NoError(t, f.db.NewSelect().Model(after).WherePK().Scan(ctx))
	require.NotNil(t, after.LastMessageID, "summary survived rollback")
	require.Equal(t, *before.LastMessageID, *after.LastMessageID, "summary survived rollback")
	require.True(t, after.UpdatedAt.Equal(before.UpdatedAt), "summary survived rollback: before=%+v after=%+v", before, after)
	assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, runtime).Execute(ctx, f.owner, input)
	require.NoError(t, err)
	var identityID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_message_deliveries").Column("channel_identity_id").Where("message_id = ?", message.ID).Scan(ctx, &identityID))
	wakeups := servertest.QueuedInputs(t, runtime, deliveryaction.AdvanceActionName, func(input deliveryaction.AdvanceInput) bool { return input.ChannelIdentityID == identityID })
	require.NotEmpty(t, wakeups, "missing atomic wakeup")
}

// TestChannelDeliveryBotMessageNamespace 验证平台消息编号按机器人隔离。
func TestChannelDeliveryBotMessageNamespace(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "旧机器人回复", uuid.NewV7().String())
	f.execute(t, first.ID)
	require.NoError(t, connectTestTelegramBot(ctx, f.db, f.channelID, 456, "456:token"))
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(ctx, f.channelID, telegramUpdate{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "新机器人首条消息", OriginatedAt: time.Now().UTC()}}))
	exists, err := f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND body = ?", f.conversationID, "新机器人首条消息").Exists(ctx)
	require.NoError(t, err)
	require.True(t, exists, "new bot inbound lost")
	// 新机器人可以返回与旧机器人相同的平台消息编号。
	f.sender.bodies = nil
	second := f.send(t, "新机器人回复", uuid.NewV7().String())
	got := f.execute(t, second.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status)
	require.Equal(t, "1001", f.providerMessageID(t, got.ID))
}

// TestChannelDeliveryCurrentCapabilities 验证暂停状态和重试入口随当前渠道变化。
func TestChannelDeliveryCurrentCapabilities(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "待确认消息", uuid.NewV7().String())
	_, err := f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'needs_review', last_error = 'unknown_result' WHERE id = ?", first.ID)
	require.NoError(t, err)
	second := f.send(t, "等待发送", uuid.NewV7().String())
	// deliveries 读取两条消息在成员消息窗口中的投递状态。
	deliveries := func() (*conversationaction.MessageDelivery, *conversationaction.MessageDelivery) {
		return readWindowMessage(t, f.db, f.owner, f.conversationID, first.MessageID).Delivery, readWindowMessage(t, f.db, f.owner, f.conversationID, second.MessageID).Delivery
	}
	firstState, secondState := deliveries()
	require.NotNil(t, firstState)
	require.NotNil(t, secondState)
	require.Equal(t, first.ID, firstState.ID)
	require.Equal(t, second.ID, secondState.ID)
	require.True(t, firstState.CanRetry, "initial capabilities incorrect")
	require.False(t, secondState.Paused, "initial capabilities incorrect")
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET enabled = false WHERE id = ?", f.channelID)
	require.NoError(t, err)
	firstState, secondState = deliveries()
	require.False(t, firstState.CanRetry, "disabled capabilities")
	require.True(t, secondState.Paused, "disabled capabilities")
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channelID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET provider_account_id = '456' WHERE id = ?", f.channelID)
	require.NoError(t, err)
	firstState, _ = deliveries()
	require.False(t, firstState.CanRetry, "changed bot capabilities")
	require.Equal(t, domain.ChannelDeliveryNeedsReview, firstState.Status, "changed bot capabilities")
}

// sendAttachment 保存一条客服附件消息，登记其存储内容并读取对应投递。
func (f channelDeliveryFixture) sendAttachment(t *testing.T, input servicesessionaction.ServiceAttachmentMessageInput, content string) models.ChannelMessageDelivery {
	t.Helper()
	ctx := context.Background()
	input.ConversationID, input.ClientMessageID = f.conversationID, uuid.NewV7().String()
	message, err := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, input)
	require.NoError(t, err)
	var storageKey string
	require.NoError(t, f.db.NewSelect().Table("files").Column("storage_key").Where("id = ?", input.FileID).Scan(ctx, &storageKey))
	if content != "" {
		f.files[storageKey] = content
	}
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("message_id = ?", message.ID).Scan(ctx))
	return delivery
}

// TestChannelDeliveryMedia 验证附件消息携带文件内容、说明和引用送达 Telegram，并与文本保持同一发送顺序。
func TestChannelDeliveryMedia(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	text := f.send(t, "先发文字", uuid.NewV7().String())
	// 引用客户入站的首条消息。
	var inboundID string
	require.NoError(t, f.db.NewSelect().Table("channel_messages").Column("message_id").Where("conversation_id = ? AND provider_message_id = '1'", f.conversationID).Scan(ctx, &inboundID))
	photo := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{
		FileID: uploadedAttachment(t, f.db, f.owner, "截图.png", "image/png"), Body: "请看截图", ReplyToMessageID: inboundID, ImageWidth: 320, ImageHeight: 200,
	}, "png-bytes")
	// 附件排在文本之后发送。
	require.Equal(t, domain.ChannelDeliveryPending, f.execute(t, photo.ID).Status, "media overtook text")
	require.Empty(t, f.sender.media, "media overtook text")
	require.Equal(t, domain.ChannelDeliverySent, f.load(t, text.ID).Status)
	got := f.execute(t, photo.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status)
	require.Equal(t, "1002", f.providerMessageID(t, got.ID))
	require.Equal(t, []sentMedia{{"截图.png", "image/png", "png-bytes", 320, 200}}, f.sender.media)
	require.Equal(t, "请看截图", f.sender.bodies[1])
	require.NotNil(t, f.sender.replies[1])
	require.Equal(t, "1", *f.sender.replies[1])
	// 没有说明和引用的附件只携带文件。
	plain := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, "合同.pdf", "application/pdf")}, "pdf-bytes")
	require.Equal(t, domain.ChannelDeliverySent, f.execute(t, plain.ID).Status)
	require.Empty(t, f.sender.bodies[2])
	require.Nil(t, f.sender.replies[2])
	require.Equal(t, "pdf-bytes", f.sender.media[1].content)
	// 送达的附件取得平台消息映射，可被后续消息引用。
	mapped, err := f.db.NewSelect().Table("channel_messages").Where("message_id = ? AND provider_message_id = '1002'", photo.MessageID).Exists(ctx)
	require.NoError(t, err)
	require.True(t, mapped)
}

// TestChannelDeliveryMediaFailures 验证媒体发送的平台拒绝、结果未知和内容不可读各自进入对应状态。
func TestChannelDeliveryMediaFailures(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	input := func(name string) servicesessionaction.ServiceAttachmentMessageInput {
		return servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, name, "application/pdf")}
	}
	// 平台明确拒绝进入失败并可重试。
	f.sender.err = &telegram.SendError{Code: "message_rejected"}
	rejected := f.sendAttachment(t, input("拒绝.pdf"), "pdf")
	got := f.execute(t, rejected.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status)
	require.Equal(t, "message_rejected", got.LastError)
	// 网络失败无法确认平台是否受理，等待人工确认。
	f.sender.err = &telegram.SendError{Code: "unknown_result"}
	unknown := f.sendAttachment(t, input("未知.pdf"), "pdf")
	got = f.execute(t, unknown.ID)
	require.Equal(t, domain.ChannelDeliveryUncertain, got.Status)
	require.NotNil(t, got.UncertainUntil)
	_, err := f.db.ExecContext(context.Background(), "UPDATE channel_message_deliveries SET status = 'failed' WHERE id = ?", unknown.ID)
	require.NoError(t, err)
	// 存储内容不可读时平台未收到请求，投递直接失败。
	f.sender.err = nil
	calls := len(f.sender.media)
	missing := f.sendAttachment(t, input("缺失.pdf"), "")
	got = f.execute(t, missing.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status)
	require.Equal(t, "attachment_unavailable", got.LastError)
	require.Len(t, f.sender.media, calls)
}
