//go:build server

package integrationtest

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	deliveryaction "github.com/runforyou-ai/cervi/internal/actions/customerdelivery"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

type customerDeliveryFixture struct {
	customerReadFixture
	sender *deliverySender
	worker *deliveryaction.Worker
	files  deliveryFiles
}
type deliverySender struct {
	mu      sync.Mutex
	bodies  []string
	replies []*string
	media   []sentMedia
	err     error
	// onMedia 在媒体发送进行中回调，供测试观察认领状态。
	onMedia func()
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

// newCustomerDeliveryFixture 建立已配置的 Telegram 私聊和两个客服身份。
func newCustomerDeliveryFixture(t *testing.T) customerDeliveryFixture {
	t.Helper()
	f := newCustomerReadFixture(t)
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), "DELETE FROM customer_message_deliveries WHERE organization_id = ?", f.owner.Organization.ID)
		_, _ = f.db.ExecContext(context.Background(), "DELETE FROM customer_channel_send_gates WHERE organization_id = ?", f.owner.Organization.ID)
	})
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: "Telegram 投递测试", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.channelID = channel.ID
	if _, err := f.db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_id = 123, bot_token = '123:token', webhook_secret = 'secret' WHERE channel_id = ?", channel.ID); err != nil {
		t.Fatal(err)
	}
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	if err := receiver.Execute(ctx, channel.ID, customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "你好", OriginatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cc.conversation_id").Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id").Where("cci.channel_id = ?", channel.ID).Scan(ctx, &f.conversationID); err != nil {
		t.Fatal(err)
	}
	sender := &deliverySender{}
	runtime := newTestTasks(f.db)
	files := deliveryFiles{}
	worker := deliveryaction.NewWorker(f.db, sender, files, runtime)
	if err := runtime.Registry().RegisterJSON(deliveryaction.SendActionName, worker.Execute); err != nil {
		t.Fatal(err)
	}
	return customerDeliveryFixture{f, sender, worker, files}
}

// send 保存一条客服消息并读取对应投递。
func (f customerDeliveryFixture) send(t *testing.T, body, clientID string) models.CustomerMessageDelivery {
	t.Helper()
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(context.Background(), f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: clientID, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.CustomerMessageDelivery
	if err := f.db.NewSelect().Model(&delivery).Where("message_id = ?", message.ID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return delivery
}

// load 读取投递的持久状态。
func (f customerDeliveryFixture) load(t *testing.T, id string) models.CustomerMessageDelivery {
	t.Helper()
	var delivery models.CustomerMessageDelivery
	if err := f.db.NewSelect().Model(&delivery).Where("id = ?", id).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return delivery
}

// execute 运行一次投递并重新读取状态。
func (f customerDeliveryFixture) execute(t *testing.T, id string) models.CustomerMessageDelivery {
	t.Helper()
	if err := f.worker.Execute(context.Background(), deliveryaction.Input{DeliveryID: id}); err != nil {
		t.Fatal(err)
	}
	return f.load(t, id)
}

// TestCustomerDeliveryFIFO 验证幂等入队、并发认领与身份顺序。
func TestCustomerDeliveryFIFO(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	clientID := uuid.NewV7().String()
	first := f.send(t, "第一条", clientID)
	replay := f.send(t, "第一条", clientID)
	if first.ID != replay.ID {
		t.Fatal("duplicate delivery")
	}
	second := f.send(t, "第二条", uuid.NewV7().String())
	if got := f.execute(t, second.ID); got.Status != domain.CustomerDeliveryPending || len(f.sender.bodies) != 0 {
		t.Fatal("queue skipped head")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- f.worker.Execute(context.Background(), deliveryaction.Input{DeliveryID: first.ID}) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := f.execute(t, second.ID); got.Status != domain.CustomerDeliverySent || got.ProviderMessageID == nil {
		t.Fatalf("delivery=%+v", got)
	}
	if len(f.sender.bodies) != 2 || f.sender.bodies[0] != "第一条" || f.sender.bodies[1] != "第二条" {
		t.Fatalf("calls=%v", f.sender.bodies)
	}
}

// TestCustomerDeliveryIdentitiesSendInParallel 验证同一渠道的一位客户的发送进行中时，其他客户的投递照常发送。
func TestCustomerDeliveryIdentitiesSendInParallel(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	if err := receiver.Execute(ctx, f.channelID, customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{ChatID: 67890, SenderID: 67890, MessageID: 1, DisplayName: "另一位客户", Body: "在吗", OriginatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	other := f
	if err := f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cc.conversation_id").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id").
		Where("cci.channel_id = ? AND cci.external_id = ?", f.channelID, "67890").Scan(ctx, &other.conversationID); err != nil {
		t.Fatal(err)
	}
	media := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, "视频.mp4", "video/mp4")}, "mp4")
	text := other.send(t, "另一位客户的回复", uuid.NewV7().String())
	var parallel models.CustomerMessageDelivery
	f.sender.onMedia = func() {
		parallel = f.execute(t, text.ID)
	}
	if got := f.execute(t, media.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatalf("media=%+v", got)
	}
	if parallel.Status != domain.CustomerDeliverySent {
		t.Fatalf("parallel=%+v", parallel)
	}
}

// TestTelegramFirstInboundConcurrent 验证新客户的多条首批消息并发到达时全部入站并归入同一渠道身份。
func TestTelegramFirstInboundConcurrent(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	for customer := range int64(5) {
		chatID := 70000 + customer
		var wg sync.WaitGroup
		errs := make(chan error, 3)
		for message := range int64(3) {
			wg.Go(func() {
				errs <- receiver.Execute(ctx, f.channelID, customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: chatID*10 + message, Message: &telegram.InboundMessage{
					ChatID: chatID, SenderID: chatID, MessageID: message + 1, DisplayName: "并发客户", Body: "你好", OriginatedAt: time.Now().UTC(),
				}})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		count, err := f.db.NewSelect().TableExpr("messages AS m").
			Join("JOIN channel_conversations AS cc ON cc.conversation_id = m.conversation_id").
			Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id").
			Where("cci.channel_id = ? AND cci.external_id = ? AND m.type = ?", f.channelID, strconv.FormatInt(chatID, 10), domain.MessageTypeText).Count(ctx)
		if err != nil || count != 3 {
			t.Fatalf("customer %d messages=%d err=%v", chatID, count, err)
		}
	}
}

// TestCustomerDeliveryWakesNextHead 验证完成发送后为同一渠道身份的下一个到期队头创建发送任务，该投递已有运行中的任务时同样创建，未完成发送的执行不创建。
func TestCustomerDeliveryWakesNextHead(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "第一条", uuid.NewV7().String())
	second := f.send(t, "第二条", uuid.NewV7().String())
	// 第二条入队时的任务正在运行：因队头未完成而未认领、尚未结束。
	if _, err := f.db.ExecContext(ctx, "UPDATE task_runs SET status = 'running' WHERE idempotency_key = ?", "cdeliv-item:"+second.ID); err != nil {
		t.Fatal(err)
	}
	queued := func() int {
		t.Helper()
		count, err := f.db.NewSelect().TableExpr("task_runs").
			Where("action_name = ? AND status = 'queued' AND payload->>'deliveryId' = ?", deliveryaction.SendActionName, second.ID).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatalf("first=%+v", got)
	}
	if count := queued(); count != 1 {
		t.Fatalf("next head wakeups = %d", count)
	}
	f.execute(t, first.ID)
	if count := queued(); count != 1 {
		t.Fatalf("wakeups after repeated execution = %d", count)
	}
}

// TestCustomerDeliveryUnknownRecovery 验证未知结果阻塞、人工重试排队与风险确认。
func TestCustomerDeliveryUnknownRecovery(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "结果未知", uuid.NewV7().String())
	second := f.send(t, "后续消息", uuid.NewV7().String())
	f.sender.err = &telegram.SendError{Code: "unknown_result"}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliveryUncertain {
		t.Fatal(got.Status)
	}
	f.execute(t, first.ID)
	f.execute(t, second.ID)
	if len(f.sender.bodies) != 1 {
		t.Fatal("unknown result automatically resent")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET uncertain_until = now() - interval '1 second' WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliveryNeedsReview {
		t.Fatal(got.Status)
	}
	manager := deliveryaction.NewManager(f.db, nil)
	if err := manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.CustomerDeliveryRetry, false); !errors.Is(err, deliveryaction.ErrConflict) {
		t.Fatalf("risk confirmation=%v", err)
	}
	if err := manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.CustomerDeliveryRetry, true); err != nil {
		t.Fatal(err)
	}
	if f.load(t, first.ID).Position <= second.Position {
		t.Fatal("retry not at tail")
	}
	f.sender.err = nil
	f.execute(t, first.ID)
	if len(f.sender.bodies) != 1 {
		t.Fatal("retry skipped queued message")
	}
	f.execute(t, second.ID)
	f.execute(t, first.ID)
	if len(f.sender.bodies) != 3 || f.sender.bodies[1] != "后续消息" {
		t.Fatal(f.sender.bodies)
	}
}

// TestCustomerDeliveryLeaseAndLifecycle 验证过期认领、渠道停用和机器人变化。
func TestCustomerDeliveryLeaseAndLifecycle(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "中断消息", uuid.NewV7().String())
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET status = 'sending', lease_worker = ?, lease_expires_at = now() - interval '1 second' WHERE id = ?", uuid.NewV7().String(), first.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliveryUncertain || len(f.sender.bodies) != 0 {
		t.Fatal("expired sending was resent")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET uncertain_until = now() - interval '1 second' WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	f.execute(t, first.ID)
	second := f.send(t, "等待恢复", uuid.NewV7().String())
	if _, err := f.db.ExecContext(ctx, "UPDATE channels SET enabled = false WHERE id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, second.ID); got.Status != domain.CustomerDeliveryPending || got.LastError != "" {
		t.Fatalf("paused=%+v", got)
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, second.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatal(got.Status)
	}
	third := f.send(t, "旧机器人消息", uuid.NewV7().String())
	if _, err := f.db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_id = 456 WHERE channel_id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, third.ID); got.Status != domain.CustomerDeliveryFailed || got.LastError != "bot_changed" || len(f.sender.bodies) != 1 {
		t.Fatalf("changed bot=%+v", got)
	}
}

// TestCustomerDeliveryRateLimitAndIsolation 验证渠道等待、永久拒绝和企业隔离。
func TestCustomerDeliveryRateLimitAndIsolation(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "限流消息", uuid.NewV7().String())
	f.sender.err = &telegram.SendError{Code: "rate_limited", RetryAfter: time.Minute}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliveryRetryWait {
		t.Fatal(got.Status)
	}
	f.execute(t, first.ID)
	if len(f.sender.bodies) != 1 {
		t.Fatal("ignored rate limit")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET available_at = now() - interval '1 second' WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	f.execute(t, first.ID)
	if len(f.sender.bodies) != 1 {
		t.Fatal("ignored channel gate")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_channel_send_gates SET flood_wait_until = now() - interval '1 second' WHERE channel_id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	f.sender.err = &telegram.SendError{Code: "recipient_unavailable"}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliveryFailed {
		t.Fatal(got.Status)
	}
	if rows, err := deliveryaction.ListForMessages(ctx, f.db, uuid.NewV7().String(), f.conversationID, []string{first.MessageID}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-organization delivery visible: %+v err=%v", rows, err)
	}
	manager := deliveryaction.NewManager(f.db, nil)
	f.sender.err = nil
	if err := manager.Resolve(ctx, f.owner, f.conversationID, first.ID, domain.CustomerDeliveryRetry, false); err != nil {
		t.Fatal(err)
	}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatal(got.Status)
	}
}

// TestCustomerDeliveryScanAndManualConfirmation 验证没有快速唤醒时扫描恢复以及人工确认。
func TestCustomerDeliveryScanAndManualConfirmation(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "扫描恢复", uuid.NewV7().String())
	// 按数据库当前时间设置到期时间。
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET available_at = now() - interval '1 second' WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Scan(ctx, struct{}{}); err != nil {
		t.Fatal(err)
	}
	// 扫描只创建唤醒，模拟 Worker 消费后才调用平台。
	if len(f.sender.bodies) != 0 {
		t.Fatal("scanner called Telegram")
	}
	if exists, err := f.db.NewSelect().TableExpr("task_runs").Where("idempotency_key = ?", "cdeliv-item:"+first.ID).Exists(ctx); err != nil || !exists {
		t.Fatalf("scan wakeup: %v %v", exists, err)
	}
	if got := f.execute(t, first.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatal(got.Status)
	}
	second := f.send(t, "人工确认", uuid.NewV7().String())
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET status = 'needs_review', last_error = 'unknown_result' WHERE id = ?", second.ID); err != nil {
		t.Fatal(err)
	}
	manager := deliveryaction.NewManager(f.db, nil)
	if err := manager.Resolve(ctx, f.owner, f.conversationID, second.ID, domain.CustomerDeliveryConfirmSent, false); err != nil {
		t.Fatal(err)
	}
	got := f.load(t, second.ID)
	if got.Status != domain.CustomerDeliverySent || got.ProviderMessageID != nil || got.LastError != "manually_confirmed" {
		t.Fatalf("manual confirmation=%+v", got)
	}
	if err := manager.Resolve(ctx, f.owner, f.conversationID, second.ID, domain.CustomerDeliveryRetry, true); !errors.Is(err, deliveryaction.ErrConflict) {
		t.Fatal("stale operation accepted")
	}
	other := newCustomerDeliveryFixture(t)
	if err := manager.Resolve(ctx, other.owner, f.conversationID, second.ID, domain.CustomerDeliveryConfirmFailed, false); !errors.Is(err, deliveryaction.ErrUnavailable) {
		t.Fatal("cross-organization operation accepted")
	}
}

type failingDeliveryEnqueuer struct {
	observedAtomicRows bool
	inner              servertask.TxEnqueuer
	taskID             string
}

// EnqueueIn 检查业务行与投递行已进入同一事务，再模拟唤醒写入失败。
func (e *failingDeliveryEnqueuer) EnqueueIn(ctx context.Context, tx bun.IDB, action string, input any, options servertask.EnqueueOptions) (string, error) {
	id := input.(deliveryaction.Input).DeliveryID
	var err error
	e.observedAtomicRows, err = tx.NewSelect().TableExpr("customer_message_deliveries AS d").Join("JOIN messages AS m ON m.id = d.message_id AND m.organization_id = d.organization_id").Join("JOIN conversations cv ON cv.id = m.conversation_id AND cv.last_message_id = m.id").Join("JOIN service_sessions ss ON ss.id = m.service_session_id AND ss.last_message_id = m.id").Where("d.id = ?", id).Exists(ctx)
	if err != nil {
		return "", err
	}
	e.taskID, err = e.inner.EnqueueIn(ctx, tx, action, input, options)
	if err != nil {
		return "", err
	}
	return "", errors.New("enqueue failed")
}

// EnqueueManyIn 转交内部任务运行时。
func (e *failingDeliveryEnqueuer) EnqueueManyIn(ctx context.Context, tx bun.IDB, requests []servertask.EnqueueRequest) ([]string, error) {
	return e.inner.EnqueueManyIn(ctx, tx, requests)
}

// TestCustomerDeliveryAtomicEnqueue 验证唤醒失败回滚消息与投递，成功时提交可靠任务。
func TestCustomerDeliveryAtomicEnqueue(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "必须原子提交"}
	runtime := newTestTasks(f.db)
	if err := runtime.Registry().RegisterJSON(deliveryaction.SendActionName, f.worker.Execute); err != nil {
		t.Fatal(err)
	}
	before := &models.Conversation{ID: f.conversationID}
	if err := f.db.NewSelect().Model(before).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	failing := &failingDeliveryEnqueuer{inner: runtime}
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, failing).Execute(ctx, f.owner, input); err == nil || !failing.observedAtomicRows {
		t.Fatalf("atomic rows=%v err=%v", failing.observedAtomicRows, err)
	}
	if exists, err := f.db.NewSelect().TableExpr("customer_message_deliveries").Where("conversation_id = ?", f.conversationID).Exists(ctx); err != nil || exists {
		t.Fatalf("delivery survived rollback: %v %v", exists, err)
	}
	if exists, err := f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND body = ?", f.conversationID, input.Body).Exists(ctx); err != nil || exists {
		t.Fatalf("message survived rollback: %v %v", exists, err)
	}
	if failing.taskID == "" {
		t.Fatal("task was not written before rollback")
	}
	for table, column := range map[string]string{"task_runs": "id", "task_outbox": "task_run_id"} {
		count, err := f.db.NewSelect().TableExpr(table).Where("? = ?", bun.Ident(column), failing.taskID).Count(ctx)
		if err != nil || count != 0 {
			t.Fatalf("rollback %s rows=%d err=%v", table, count, err)
		}
	}
	after := &models.Conversation{ID: f.conversationID}
	if err := f.db.NewSelect().Model(after).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if after.LastMessageID == nil || *after.LastMessageID != *before.LastMessageID || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("summary survived rollback: before=%+v after=%+v", before, after)
	}
	assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, runtime).Execute(ctx, f.owner, input)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := f.db.NewSelect().TableExpr("customer_message_deliveries AS d").Join("JOIN task_runs AS tr ON tr.idempotency_key = 'cdeliv-item:' || d.id::text").Join("JOIN task_outbox AS tob ON tob.task_run_id = tr.id").Where("d.message_id = ?", message.ID).Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("missing atomic wakeup: %v %v", exists, err)
	}
}

// TestCustomerDeliveryBotMessageNamespace 验证平台消息编号按机器人隔离。
func TestCustomerDeliveryBotMessageNamespace(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "旧机器人回复", uuid.NewV7().String())
	f.execute(t, first.ID)
	if _, err := f.db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_id = 456, bot_token = '456:token' WHERE channel_id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	receiver := customerchataction.NewReceiveTelegramWebhookAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), domain.FileStorageBackendLocal, newTestTasks(f.db))
	if err := receiver.Execute(ctx, f.channelID, customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: 1, DisplayName: "Telegram 客户", Body: "新机器人首条消息", OriginatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	exists, err := f.db.NewSelect().TableExpr("messages").Where("conversation_id = ? AND body = ?", f.conversationID, "新机器人首条消息").Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("new bot inbound lost: %v %v", exists, err)
	}
	// 新机器人可以返回与旧机器人相同的平台消息编号。
	f.sender.bodies = nil
	second := f.send(t, "新机器人回复", uuid.NewV7().String())
	got := f.execute(t, second.ID)
	if got.Status != domain.CustomerDeliverySent || got.ProviderMessageID == nil || *got.ProviderMessageID != 1001 {
		t.Fatalf("new bot result=%+v", got)
	}
}

// TestCustomerDeliveryCurrentCapabilities 验证暂停状态和重试入口随当前渠道变化。
func TestCustomerDeliveryCurrentCapabilities(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	first := f.send(t, "待确认消息", uuid.NewV7().String())
	if _, err := f.db.ExecContext(ctx, "UPDATE customer_message_deliveries SET status = 'needs_review', last_error = 'unknown_result' WHERE id = ?", first.ID); err != nil {
		t.Fatal(err)
	}
	second := f.send(t, "等待发送", uuid.NewV7().String())
	// deliveries 读取两条消息在成员消息窗口中的投递状态。
	deliveries := func() (*conversationaction.MessageDelivery, *conversationaction.MessageDelivery) {
		return readWindowMessage(t, f.db, f.owner, f.conversationID, first.MessageID).Delivery, readWindowMessage(t, f.db, f.owner, f.conversationID, second.MessageID).Delivery
	}
	firstState, secondState := deliveries()
	if firstState == nil || secondState == nil || firstState.ID != first.ID || secondState.ID != second.ID {
		t.Fatalf("deliveries=%+v %+v", firstState, secondState)
	}
	if !firstState.CanRetry || secondState.Paused {
		t.Fatal("initial capabilities incorrect")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE channels SET enabled = false WHERE id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if firstState, secondState = deliveries(); firstState.CanRetry || !secondState.Paused {
		t.Fatalf("disabled capabilities=%+v %+v", firstState, secondState)
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE telegram_channel_settings SET bot_id = 456 WHERE channel_id = ?", f.channelID); err != nil {
		t.Fatal(err)
	}
	if firstState, _ = deliveries(); firstState.CanRetry || firstState.Status != domain.CustomerDeliveryNeedsReview {
		t.Fatalf("changed bot capabilities=%+v", firstState)
	}
}

// sendAttachment 保存一条客服附件消息，登记其存储内容并读取对应投递。
func (f customerDeliveryFixture) sendAttachment(t *testing.T, input servicesessionaction.ServiceAttachmentMessageInput, content string) models.CustomerMessageDelivery {
	t.Helper()
	ctx := context.Background()
	input.ConversationID, input.ClientMessageID = f.conversationID, uuid.NewV7().String()
	message, err := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, nil).Execute(ctx, f.owner, input)
	if err != nil {
		t.Fatal(err)
	}
	var storageKey string
	if err := f.db.NewSelect().Table("files").Column("storage_key").Where("id = ?", input.FileID).Scan(ctx, &storageKey); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		f.files[storageKey] = content
	}
	var delivery models.CustomerMessageDelivery
	if err := f.db.NewSelect().Model(&delivery).Where("message_id = ?", message.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return delivery
}

// TestCustomerDeliveryMedia 验证附件消息携带文件内容、说明和引用送达 Telegram，并与文本保持同一发送顺序。
func TestCustomerDeliveryMedia(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	ctx := context.Background()
	text := f.send(t, "先发文字", uuid.NewV7().String())
	// 引用客户入站的首条消息。
	var inboundID string
	if err := f.db.NewSelect().Table("channel_messages").Column("message_id").Where("conversation_id = ? AND provider_message_id = '1'", f.conversationID).Scan(ctx, &inboundID); err != nil {
		t.Fatal(err)
	}
	photo := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{
		FileID: uploadedAttachment(t, f.db, f.owner, "截图.png", "image/png"), Body: "请看截图", ReplyToMessageID: inboundID, ImageWidth: 320, ImageHeight: 200,
	}, "png-bytes")
	// 队头文本未完成时附件不越过发送。
	if got := f.execute(t, photo.ID); got.Status != domain.CustomerDeliveryPending || len(f.sender.media) != 0 {
		t.Fatalf("media overtook text: %+v", got)
	}
	f.execute(t, text.ID)
	got := f.execute(t, photo.ID)
	if got.Status != domain.CustomerDeliverySent || got.ProviderMessageID == nil || *got.ProviderMessageID != 1002 {
		t.Fatalf("media result=%+v", got)
	}
	if len(f.sender.media) != 1 || f.sender.media[0] != (sentMedia{"截图.png", "image/png", "png-bytes", 320, 200}) {
		t.Fatalf("media=%+v", f.sender.media)
	}
	if f.sender.bodies[1] != "请看截图" || f.sender.replies[1] == nil || *f.sender.replies[1] != "1" {
		t.Fatalf("caption=%q reply=%v", f.sender.bodies[1], f.sender.replies[1])
	}
	// 没有说明和引用的附件只携带文件。
	plain := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, "合同.pdf", "application/pdf")}, "pdf-bytes")
	if got := f.execute(t, plain.ID); got.Status != domain.CustomerDeliverySent || f.sender.bodies[2] != "" || f.sender.replies[2] != nil || f.sender.media[1].content != "pdf-bytes" {
		t.Fatalf("plain result=%+v media=%+v", got, f.sender.media)
	}
	// 送达的附件取得平台消息映射，可被后续消息引用。
	mapped, err := f.db.NewSelect().Table("channel_messages").Where("message_id = ? AND provider_message_id = '1002'", photo.MessageID).Exists(ctx)
	if err != nil || !mapped {
		t.Fatalf("mapped=%t err=%v", mapped, err)
	}
}

// TestCustomerDeliveryMediaFailures 验证媒体发送的平台拒绝、结果未知和内容不可读各自进入对应状态。
func TestCustomerDeliveryMediaFailures(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	input := func(name string) servicesessionaction.ServiceAttachmentMessageInput {
		return servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, name, "application/pdf")}
	}
	// 平台明确拒绝进入失败并可重试。
	f.sender.err = &telegram.SendError{Code: "message_rejected"}
	rejected := f.sendAttachment(t, input("拒绝.pdf"), "pdf")
	if got := f.execute(t, rejected.ID); got.Status != domain.CustomerDeliveryFailed || got.LastError != "message_rejected" {
		t.Fatalf("rejected=%+v", got)
	}
	// 网络失败无法确认平台是否受理，等待人工确认。
	f.sender.err = &telegram.SendError{Code: "unknown_result"}
	unknown := f.sendAttachment(t, input("未知.pdf"), "pdf")
	if got := f.execute(t, unknown.ID); got.Status != domain.CustomerDeliveryUncertain || got.UncertainUntil == nil {
		t.Fatalf("unknown=%+v", got)
	}
	if _, err := f.db.ExecContext(context.Background(), "UPDATE customer_message_deliveries SET status = 'failed' WHERE id = ?", unknown.ID); err != nil {
		t.Fatal(err)
	}
	// 存储内容不可读时平台未收到请求，投递直接失败。
	f.sender.err = nil
	calls := len(f.sender.media)
	missing := f.sendAttachment(t, input("缺失.pdf"), "")
	if got := f.execute(t, missing.ID); got.Status != domain.CustomerDeliveryFailed || got.LastError != "attachment_unavailable" || len(f.sender.media) != calls {
		t.Fatalf("missing=%+v calls=%d", got, len(f.sender.media))
	}
	// 文件已清理或附件记录缺失的附件消息同样失败，带说明时也不按文本发出。
	ctx := context.Background()
	cleaned := f.sendAttachment(t, input("已清理.pdf"), "pdf")
	if _, err := f.db.ExecContext(ctx, "UPDATE message_attachments SET file_id = NULL, transfer_status = 'failed' WHERE message_id = ?", cleaned.MessageID); err != nil {
		t.Fatal(err)
	}
	orphanInput := input("无记录.pdf")
	orphanInput.Body = "附件说明"
	orphan := f.sendAttachment(t, orphanInput, "pdf")
	if _, err := f.db.ExecContext(ctx, "DELETE FROM message_attachments WHERE message_id = ?", orphan.MessageID); err != nil {
		t.Fatal(err)
	}
	texts := len(f.sender.bodies)
	for _, delivery := range []models.CustomerMessageDelivery{cleaned, orphan} {
		if got := f.execute(t, delivery.ID); got.Status != domain.CustomerDeliveryFailed || got.LastError != "attachment_unavailable" {
			t.Fatalf("unavailable=%+v", got)
		}
	}
	if len(f.sender.bodies) != texts {
		t.Fatalf("attachment sent as text: %v", f.sender.bodies[texts:])
	}
}

// TestCustomerDeliveryMediaLease 验证附件投递的认领租约覆盖媒体发送超时。
func TestCustomerDeliveryMediaLease(t *testing.T) {
	t.Parallel()
	f := newCustomerDeliveryFixture(t)
	delivery := f.sendAttachment(t, servicesessionaction.ServiceAttachmentMessageInput{FileID: uploadedAttachment(t, f.db, f.owner, "视频.mp4", "video/mp4")}, "mp4")
	var lease time.Duration
	f.sender.onMedia = func() {
		if sending := f.load(t, delivery.ID); sending.LeaseExpiresAt != nil {
			lease = time.Until(*sending.LeaseExpiresAt)
		}
	}
	if got := f.execute(t, delivery.ID); got.Status != domain.CustomerDeliverySent {
		t.Fatalf("result=%+v", got)
	}
	if lease <= 5*time.Minute {
		t.Fatalf("lease=%s", lease)
	}
}
