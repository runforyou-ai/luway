//go:build server

package integrationtest

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// splitOutbound 把每条消息拆成两个请求项。
type splitOutbound struct {
	mu       sync.Mutex
	requests []channeladapter.Request
	outcome  channeladapter.Outcome
}

// Plan 把正文拆成前后两个请求项。
func (o *splitOutbound) Plan(body string, attachment bool) []channeladapter.Item {
	return []channeladapter.Item{{Body: body + "#1"}, {Body: body + "#2", Attachment: attachment}}
}

// SendTimeout 返回固定的发送超时。
func (o *splitOutbound) SendTimeout(channeladapter.Item) time.Duration { return 5 * time.Second }

// Send 记录请求并返回预设结果，发送成功时按请求次序返回平台消息编号 9001、9002……
func (o *splitOutbound) Send(_ context.Context, _ channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, request)
	if o.outcome == "" {
		return channeladapter.Result{Outcome: channeladapter.OutcomeSent, ProviderMessageID: strconv.Itoa(9000 + len(o.requests))}
	}
	return channeladapter.Result{Outcome: o.outcome}
}

// newSplitDeliveryFixture 建立 Telegram 私聊，并以两段式适配器替换外发。
func newSplitDeliveryFixture(t *testing.T) (channelDeliveryFixture, *splitOutbound, *deliveryaction.Worker) {
	t.Helper()
	f := newChannelDeliveryFixture(t)
	outbound := &splitOutbound{}
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeTelegram, outbound)
	return f, outbound, deliveryaction.NewWorker(f.db, adapters, f.files, testEnqueuer)
}

// newWindowedDeliveryFixture 建立私聊后把渠道改为受回复窗口限制的公众号渠道，并以两段式适配器替换外发；返回发起人的渠道身份。
func newWindowedDeliveryFixture(t *testing.T) (channelDeliveryFixture, *splitOutbound, *deliveryaction.Worker, string) {
	t.Helper()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	_, err := f.db.ExecContext(ctx, "UPDATE channels SET type = ?, provider_account_id = ? WHERE id = ?", domain.ChannelTypeWechatKey, "wx"+strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16], f.channelID)
	require.NoError(t, err)
	var identityID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_identities").Column("id").Where("channel_id = ?", f.channelID).Scan(ctx, &identityID))
	outbound := &splitOutbound{}
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeWechatKey, outbound)
	return f, outbound, deliveryaction.NewWorker(f.db, adapters, f.files, testEnqueuer), identityID
}

// replyWindow 读取渠道身份指定触发动作最新开启的回复窗口。
func (f channelDeliveryFixture) replyWindow(t *testing.T, identityID, trigger string) models.ChannelReplyWindow {
	t.Helper()
	var window models.ChannelReplyWindow
	require.NoError(t, f.db.NewSelect().Model(&window).Where("channel_identity_id = ? AND trigger = ?", identityID, trigger).OrderExpr("opened_at DESC").Limit(1).Scan(context.Background()))
	return window
}

// runDelivery 推进一次投递所属渠道身份的管道并重新读取该投递。
func runDelivery(t *testing.T, f channelDeliveryFixture, worker *deliveryaction.Worker, id string) models.ChannelMessageDelivery {
	t.Helper()
	advanceDelivery(t, f.db, worker, id)
	return f.load(t, id)
}

// openWindow 为渠道身份开启 message 触发动作的回复窗口。
func (f channelDeliveryFixture) openWindow(t *testing.T, identityID string, openedAt time.Time, quota int) {
	t.Helper()
	require.NoError(t, f.db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := deliveryaction.OpenReplyWindow(ctx, tx, f.owner.Workspace.ID, identityID, channeladapter.Window{
			Trigger: "message", OpenedAt: openedAt, ExpiresAt: openedAt.Add(time.Hour), Quota: &quota,
		})
		return err
	}))
}

// TestChannelConversationSendableFacts 验证平台渠道服务会话的发送资格按收件人绑定、回复窗口、渠道启用与平台账号连接判定。
func TestChannelConversationSendableFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 不受回复窗口限制的 Telegram 渠道无需窗口即可回复。
	telegramFixture := newChannelDeliveryFixture(t)
	send, err := conversationaccess.CheckSendable(ctx, telegramFixture.db, telegramFixture.owner, telegramFixture.conversationID)
	require.NoError(t, err)
	require.Equal(t, conversationaccess.SendRoleServiceReply, send.Role)
	require.Equal(t, conversationaccess.DenialNone, send.Denial)

	f, _, _, identityID := newWindowedDeliveryFixture(t)
	check := func(want conversationaccess.Denial, step string) {
		t.Helper()
		send, err := conversationaccess.CheckSendable(ctx, f.db, f.owner, f.conversationID)
		require.NoError(t, err, step)
		require.Equal(t, conversationaccess.SendRoleServiceReply, send.Role, step)
		require.Equal(t, want, send.Denial, step)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := f.db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}

	check(conversationaccess.DenialChannelReplyWindowClosed, "没有回复窗口")
	f.openWindow(t, identityID, time.Now().UTC().Add(-time.Second), 5)
	check(conversationaccess.DenialNone, "回复窗口可用")
	exec("UPDATE channel_reply_windows SET expires_at = now() - interval '1 second' WHERE channel_identity_id = ?", identityID)
	check(conversationaccess.DenialChannelReplyWindowClosed, "回复窗口到期")
	f.openWindow(t, identityID, time.Now().UTC(), 5)
	check(conversationaccess.DenialNone, "重新开启回复窗口")

	var provider string
	require.NoError(t, f.db.NewSelect().TableExpr("channels").Column("provider_account_id").Where("id = ?", f.channelID).Scan(ctx, &provider))
	exec("UPDATE channels SET enabled = FALSE WHERE id = ?", f.channelID)
	check(conversationaccess.DenialChannelOutboundUnavailable, "渠道停用")
	exec("UPDATE channels SET enabled = TRUE, provider_account_id = NULL WHERE id = ?", f.channelID)
	check(conversationaccess.DenialChannelOutboundUnavailable, "渠道未连接平台账号")
	exec("UPDATE channels SET provider_account_id = ? WHERE id = ?", provider, f.channelID)
	check(conversationaccess.DenialNone, "恢复渠道")

	// 面向员工的服务会话要求渠道身份仍绑定发起成员，联系人发起的会话不满足。
	exec("UPDATE service_conversations SET audience = ? WHERE workspace_id = ? AND conversation_id = ?", domain.ServiceAudienceEmployee, f.owner.Workspace.ID, f.conversationID)
	check(conversationaccess.DenialChannelRecipientUnbound, "收件人未绑定")
}

// TestChannelDeliveryReplyWindowItems 验证整条消息一次预占窗口额度、请求项逐个发送与结算、以及额度不足时整条不发送。
func TestChannelDeliveryReplyWindowItems(t *testing.T) {
	t.Parallel()
	f, outbound, worker, identityID := newWindowedDeliveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// 没有可用窗口时拒绝发送；入队后窗口到期的消息在投递时失败。
	_, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "窗口外"})
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, conversationaction.ConflictReasonChannelReplyWindowClosed, conflict.Reason)
	f.openWindow(t, identityID, now.Add(-time.Second), 5)
	closed := f.send(t, "窗口外", uuid.NewV7().String())
	_, err = f.db.ExecContext(ctx, "UPDATE channel_reply_windows SET expires_at = now() - interval '1 second' WHERE channel_identity_id = ?", identityID)
	require.NoError(t, err)
	got := runDelivery(t, f, worker, closed.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status, "closed=%+v", got)
	require.Equal(t, "reply_window_closed", got.LastError)
	require.Empty(t, outbound.requests)
	f.openWindow(t, identityID, now, 5)

	first := f.send(t, "第一条", uuid.NewV7().String())
	got = runDelivery(t, f, worker, first.ID)
	require.Equal(t, domain.ChannelDeliveryPending, got.Status, "after first item=%+v", got)
	require.NotNil(t, got.ReplyWindowID)
	window := f.replyWindow(t, identityID, "message")
	require.Equal(t, 1, window.Used, "window after first item=%+v", window)
	require.Equal(t, 1, window.Reserved)
	got = runDelivery(t, f, worker, first.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status, "after second item=%+v", got)
	require.NotNil(t, got.SentAt)
	require.Nil(t, got.ReplyWindowID)
	require.Len(t, outbound.requests, 2)
	require.Equal(t, "第一条#1", outbound.requests[0].Body)
	require.Equal(t, "第一条#2", outbound.requests[1].Body)
	window = f.replyWindow(t, identityID, "message")
	require.Equal(t, 2, window.Used, "window=%+v", window)
	require.Equal(t, 0, window.Reserved)

	// 结果未知时保留整条预占；之后开启的新窗口不承担旧预占，人工确认计入原窗口，剩余请求项排到队尾继续。
	outbound.outcome = channeladapter.OutcomeUncertain
	second := f.send(t, "第二条", uuid.NewV7().String())
	uncertain := runDelivery(t, f, worker, second.ID)
	require.Equal(t, domain.ChannelDeliveryUncertain, uncertain.Status, "uncertain=%+v", uncertain)
	require.NotNil(t, uncertain.ReplyWindowID)
	window = f.replyWindow(t, identityID, "message")
	require.Equal(t, 2, window.Used, "reserved window=%+v", window)
	require.Equal(t, 2, window.Reserved)
	f.openWindow(t, identityID, now.Add(time.Minute), 1)
	_, err = f.db.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'needs_review' WHERE id = ?", second.ID)
	require.NoError(t, err)
	require.NoError(t, deliveryaction.NewManager(f.db, nil).Resolve(ctx, f.owner, f.conversationID, second.ID, domain.ChannelDeliveryConfirmSent, false))
	got = f.load(t, second.ID)
	require.Equal(t, domain.ChannelDeliveryPending, got.Status, "confirmed=%+v", got)
	require.Greater(t, got.Position, uncertain.Position)
	require.NotNil(t, got.ReplyWindowID)
	require.Equal(t, *uncertain.ReplyWindowID, *got.ReplyWindowID)
	var old models.ChannelReplyWindow
	require.NoError(t, f.db.NewSelect().Model(&old).Where("id = ?", *uncertain.ReplyWindowID).Scan(ctx))
	latest := f.replyWindow(t, identityID, "message")
	require.Equal(t, 3, old.Used, "old=%+v latest=%+v", old, latest)
	require.Equal(t, 1, old.Reserved)
	require.NotEqual(t, old.ID, latest.ID)
	require.Equal(t, 0, latest.Used)
	require.Equal(t, 0, latest.Reserved)
	outbound.outcome = ""
	got = runDelivery(t, f, worker, second.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status, "resumed=%+v", got)
	require.Nil(t, got.ReplyWindowID)
	require.Equal(t, "第二条#2", outbound.requests[len(outbound.requests)-1].Body, "resumed request")
	require.NoError(t, f.db.NewSelect().Model(&old).Where("id = ?", *uncertain.ReplyWindowID).Scan(ctx))
	require.Equal(t, 4, old.Used, "old after resume=%+v", old)
	require.Equal(t, 0, old.Reserved)

	// 最新窗口额度不足以发送整条消息时一个请求项都不发送，较早的开启事实不取代最新窗口。
	f.openWindow(t, identityID, now, 5)
	sent := len(outbound.requests)
	third := f.send(t, "第三条", uuid.NewV7().String())
	got = runDelivery(t, f, worker, third.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status, "insufficient=%+v", got)
	require.Equal(t, "reply_window_closed", got.LastError)
	require.Len(t, outbound.requests, sent)
	latest = f.replyWindow(t, identityID, "message")
	require.NotNil(t, latest.Quota, "latest window=%+v", latest)
	require.Equal(t, 1, *latest.Quota)
	require.Equal(t, 0, latest.Used)
	require.Equal(t, 0, latest.Reserved)
}

// TestChannelDeliveryItemsMapProviderMessages 验证每个已发送请求项都建立平台消息映射，对方引用后续分段时关联到原消息。
func TestChannelDeliveryItemsMapProviderMessages(t *testing.T) {
	t.Parallel()
	f, outbound, worker := newSplitDeliveryFixture(t)
	ctx := context.Background()
	delivery := f.send(t, "分段消息", uuid.NewV7().String())
	runDelivery(t, f, worker, delivery.ID)
	got := runDelivery(t, f, worker, delivery.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status, "delivery=%+v", got)
	require.Len(t, outbound.requests, 2)
	var parts []int
	require.NoError(t, f.db.NewSelect().TableExpr("channel_messages").Column("part").Where("message_id = ? AND provider_message_id IN ('9001', '9002')", delivery.MessageID).OrderExpr("part").Scan(ctx, &parts))
	require.Equal(t, []int{1, 2}, parts)
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(ctx, f.channelID, telegramUpdate{Secret: "secret", UpdateID: 50, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: 50, DisplayName: "Telegram 客户", Body: "回复第二段", OriginatedAt: time.Now().UTC(),
		Reply: &telegram.InboundReply{MessageID: 9002, Body: "分段消息#2", SenderName: "Bot", SenderIsBot: true},
	}}))
	var replyTo *string
	require.NoError(t, f.db.NewSelect().TableExpr("messages").Column("reply_to_message_id").Where("conversation_id = ? AND body = ?", f.conversationID, "回复第二段").Scan(ctx, &replyTo))
	require.NotNil(t, replyTo)
	require.Equal(t, delivery.MessageID, *replyTo)
}

// TestChannelDeliveryExpiredReservation 验证已预占的窗口到期后剩余请求项释放预占并改用新窗口，没有可用窗口时不再发送。
func TestChannelDeliveryExpiredReservation(t *testing.T) {
	t.Parallel()
	f, outbound, worker, identityID := newWindowedDeliveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.openWindow(t, identityID, now, 5)
	expiring := f.send(t, "到期", uuid.NewV7().String())
	first := runDelivery(t, f, worker, expiring.ID)
	require.Equal(t, domain.ChannelDeliveryPending, first.Status, "after first item=%+v", first)
	require.NotNil(t, first.ReplyWindowID)
	_, err := f.db.ExecContext(ctx, "UPDATE channel_reply_windows SET expires_at = now() - interval '1 second' WHERE id = ?", *first.ReplyWindowID)
	require.NoError(t, err)
	got := runDelivery(t, f, worker, expiring.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status, "expired=%+v", got)
	require.Equal(t, "reply_window_closed", got.LastError)
	require.Nil(t, got.ReplyWindowID)
	require.Len(t, outbound.requests, 1)
	var released models.ChannelReplyWindow
	require.NoError(t, f.db.NewSelect().Model(&released).Where("id = ?", *first.ReplyWindowID).Scan(ctx))
	require.Equal(t, 0, released.Reserved, "released=%+v", released)
	require.Equal(t, 1, released.Used)

	// 人工重试时剩余请求项改用新开启的窗口。
	f.openWindow(t, identityID, now.Add(time.Minute), 5)
	require.NoError(t, deliveryaction.NewManager(f.db, nil).Resolve(ctx, f.owner, f.conversationID, expiring.ID, domain.ChannelDeliveryRetry, false))
	got = runDelivery(t, f, worker, expiring.ID)
	require.Equal(t, domain.ChannelDeliverySent, got.Status, "retried=%+v", got)
	require.Len(t, outbound.requests, 2)
	require.Equal(t, "到期#2", outbound.requests[1].Body, "retried request")
	latest := f.replyWindow(t, identityID, "message")
	require.NotEqual(t, *first.ReplyWindowID, latest.ID, "new window=%+v", latest)
	require.Equal(t, 1, latest.Used)
	require.Equal(t, 0, latest.Reserved)
}

// windowClosedOutbound 对每次发送返回平台判定回复窗口已关闭。
type windowClosedOutbound struct{ splitOutbound }

// Send 返回回复窗口已关闭。
func (o *windowClosedOutbound) Send(context.Context, channeladapter.Target, channeladapter.Request) channeladapter.Result {
	return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: channeladapter.CodeReplyWindowClosed}
}

// TestReplyWindowClosedExpiresWindowOpenedBeforeClaim 验证认领等待渠道身份锁期间开启的回复窗口早于认领时刻，平台判定窗口已关闭后随之到期。
func TestReplyWindowClosedExpiresWindowOpenedBeforeClaim(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f, _, _, identityID := newWindowedDeliveryFixture(t)
	f.openWindow(t, identityID, time.Now().UTC().Add(-time.Minute), 5)
	delivery := f.send(t, "窗口关闭", uuid.NewV7().String())
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeWechatKey, &windowClosedOutbound{})
	worker := deliveryaction.NewWorker(f.db, adapters, f.files, testEnqueuer)

	// 持有渠道身份锁，让推进在认领前等待。
	tx, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "SELECT id FROM channel_identities WHERE id = ? FOR UPDATE", identityID)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- worker.Advance(ctx, deliveryaction.AdvanceInput{WorkspaceID: f.owner.Workspace.ID, ChannelIdentityID: identityID})
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := f.db.NewRaw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE ?)", "%"+identityID+"%").Scan(ctx, &waiting)
		return err == nil && waiting
	}, 5*time.Second, 10*time.Millisecond)

	// 推进等待期间开启新窗口后释放锁。
	_, err = deliveryaction.OpenReplyWindow(ctx, tx, f.owner.Workspace.ID, identityID, channeladapter.Window{
		Trigger: "message", OpenedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Quota: new(5),
	})
	require.NoError(t, err)
	var window models.ChannelReplyWindow
	require.NoError(t, tx.NewSelect().Model(&window).Where("channel_identity_id = ?", identityID).OrderExpr("created_at DESC").Limit(1).Scan(ctx))
	require.NoError(t, tx.Commit())
	require.NoError(t, <-done)

	got := f.load(t, delivery.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status)
	require.Equal(t, channeladapter.CodeReplyWindowClosed, got.LastError)
	var expired bool
	require.NoError(t, f.db.NewRaw("SELECT expires_at <= clock_timestamp() FROM channel_reply_windows WHERE id = ?", window.ID).Scan(ctx, &expired))
	require.True(t, expired, "认领前开启的回复窗口未到期")
}
