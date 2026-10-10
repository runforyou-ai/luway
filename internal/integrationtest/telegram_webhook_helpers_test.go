//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// telegramUpdate 是测试回调携带的 Secret、签名身份与已解析的 Update 内容。
type telegramUpdate struct {
	Secret        string
	CustomerToken string
	UpdateID      int64
	MyChatMember  bool
	Message       *telegram.InboundMessage
}

// telegramWebhook 经 Telegram 回调接收操作写入入站事件，并在投递时同步执行入站事件任务。
type telegramWebhook struct {
	receive *telegramaction.ReceiveUpdateAction
	events  *channelinboundaction.ReceiveEventAction
}

// newTelegramWebhook 创建使用 Telegram 适配器注册表的测试回调，只适用于不含媒体的消息。
func newTelegramWebhook(db *bun.DB, scheduler conversationaction.CustomerAgentMessageScheduler, tasks servertask.TxEnqueuer) *telegramWebhook {
	adapters := telegramAdapters(db, nil, nil)
	return newTelegramWebhookWith(db, adapters, scheduler, tasks, channelinboundaction.NewRetrieveMediaAction(db, adapters, nil, scheduler))
}

// newTelegramWebhookWith 创建使用指定适配器注册表与媒体取回操作的测试回调。
func newTelegramWebhookWith(db *bun.DB, adapters *channeladapter.Registry, scheduler conversationaction.CustomerAgentMessageScheduler, tasks servertask.TxEnqueuer, retrieve *channelinboundaction.RetrieveMediaAction) *telegramWebhook {
	webhook := &telegramWebhook{
		events: channelinboundaction.NewReceiveEventAction(db, adapters, scheduler, localStorage, tasks, retrieve, func() string { return servertest.PublicURL }),
	}
	webhook.receive = telegramaction.NewReceiveUpdateAction(db, webhook)
	return webhook
}

// Preflight 按真实回调在读取请求体前校验渠道和 Secret。
func (w *telegramWebhook) Preflight(ctx context.Context, channelID, secret string) error {
	return w.receive.Preflight(ctx, channelID, secret)
}

// Execute 按真实回调的认证与转换处理一次 Update。
func (w *telegramWebhook) Execute(ctx context.Context, channelID string, input telegramUpdate) error {
	return w.receive.Execute(ctx, channelID, telegramaction.UpdateInput{
		Secret: input.Secret, CustomerToken: input.CustomerToken,
		Update: telegram.Update{ID: input.UpdateID, MyChatMember: input.MyChatMember, Message: input.Message},
	})
}

// Enqueue 同步执行入站事件任务；任务执行完毕即不再处于活动状态，同一去重键再次投递时重新执行。
func (w *telegramWebhook) Enqueue(ctx context.Context, actionName string, payload any, _ servertask.EnqueueOptions) error {
	input, ok := payload.(channelinboundaction.ReceiveEventInput)
	if actionName != channelinboundaction.ReceiveEventActionName || !ok {
		return fmt.Errorf("unexpected task %s", actionName)
	}
	return w.events.Execute(ctx, input)
}

// telegramAdapters 返回只登记 Telegram 适配器的渠道适配器注册表。
func telegramAdapters(db bun.IDB, sender telegram.Sender, downloader telegram.MediaDownloader) *channeladapter.Registry {
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeTelegram, telegramaction.NewAdapter(db, sender, downloader, nil))
	return adapters
}

// queuedNotices 按投递顺序返回登记器中工作区已投递的渠道提示任务参数。
func queuedNotices(t *testing.T, tasks *servertest.Tasks, workspaceID string) []channelinboundaction.SendNoticeInput {
	t.Helper()
	runs := tasks.Queued(channelinboundaction.SendNoticeActionName, workspaceID)
	notices := make([]channelinboundaction.SendNoticeInput, len(runs))
	for index, run := range runs {
		notices[index] = servertest.TaskPayload[channelinboundaction.SendNoticeInput](t, run)
	}
	return notices
}
