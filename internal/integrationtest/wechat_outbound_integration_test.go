//go:build server

package integrationtest

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// wechatOutboundFixture 是公众号外发测试的工作区、明文模式密钥接入渠道、入站处理与投递。
type wechatOutboundFixture struct {
	customerReadFixture
	fake      *fakeWechatOpenPlatform
	channelID string
	receive   *wechataction.ReceiveMessageAction
	events    *channelinboundaction.ReceiveEventAction
	tasks     *servertest.Tasks
	worker    *deliveryaction.Worker
	files     deliveryFiles
}

// newWechatOutboundFixture 建立连接了公众号的密钥接入渠道。
func newWechatOutboundFixture(t *testing.T) *wechatOutboundFixture {
	t.Helper()
	ctx := context.Background()
	base := newCustomerReadFixture(t)
	fake := &fakeWechatOpenPlatform{responses: map[string]string{}, calls: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	channel, err := channelaction.NewCreateMessageChannelAction(base.db).Execute(ctx, base.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWechatKey, Name: "公众号外发", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	fake.set("/cgi-bin/stable_token", `{"access_token":"key-token","expires_in":7200}`)
	_, err = wechataction.NewSaveKeyConnectionAction(base.db, client, func() string { return servertest.PublicURL }).Execute(ctx, base.owner, channel.ID, wechataction.KeyConnectionInput{
		AppID: randomWechatAppID(), AppSecret: "secret", Token: "keytoken", EncryptionMode: domain.WechatEncryptionPlain,
	})
	require.NoError(t, err)
	adapters := channeladapter.NewRegistry()
	adapters.Register(domain.ChannelTypeWechatKey, wechataction.NewAdapter(base.db, client, domain.ChannelTypeWechatKey))
	tasks := servertest.NewTasks()
	files := deliveryFiles{}
	return &wechatOutboundFixture{
		customerReadFixture: base, fake: fake, channelID: channel.ID,
		receive: wechataction.NewReceiveMessageAction(base.db, tasks),
		events:  channelinboundaction.NewReceiveEventAction(base.db, adapters, &countingAgentScheduler{}, localStorage, tasks, nil, func() string { return servertest.PublicURL }),
		worker:  deliveryaction.NewWorker(base.db, adapters, files, testEnqueuer), files: files, tasks: tasks,
	}
}

// push 以明文模式推送 openID 在 at 发出的消息或事件并处理产生的入站事件任务。
func (f *wechatOutboundFixture) push(t *testing.T, openID, fields string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	query := wechat.PushQuery{Timestamp: timestamp, Nonce: "nonce", Signature: wechat.Signature("keytoken", timestamp, "nonce")}
	require.NoError(t, f.receive.ReceiveKeyMessage(ctx, f.channelID, query, []byte(wechatUserMessageAt(openID, fields, at))))
	for _, run := range f.tasks.Take(channelinboundaction.ReceiveEventActionName, f.owner.Workspace.ID) {
		require.NoError(t, f.events.Execute(ctx, servertest.TaskPayload[channelinboundaction.ReceiveEventInput](t, run)))
	}
}

// conversation 返回 openID 的会话编号。
func (f *wechatOutboundFixture) conversation(t *testing.T, openID string) string {
	t.Helper()
	var conversationID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").ColumnExpr("cc.conversation_id::text").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id").
		Where("ci.channel_id = ? AND ci.external_id = ?", f.channelID, openID).Scan(context.Background(), &conversationID))
	return conversationID
}

// replyWindow 返回会话在收件箱摘要中的回复窗口。
func (f *wechatOutboundFixture) replyWindow(t *testing.T, conversationID string) *inboxaction.ReplyWindowSummary {
	t.Helper()
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: domain.ServiceSessionStatusOpen})
	require.NoError(t, err)
	for _, row := range page.Conversations {
		if row.ID == conversationID {
			return row.Service.ReplyWindow
		}
	}
	t.Fatalf("conversation %s not in inbox", conversationID)
	return nil
}

// sendText 发送对客文本，返回投递。
func (f *wechatOutboundFixture) sendText(t *testing.T, conversationID, body string) (models.ChannelMessageDelivery, error) {
	t.Helper()
	ctx := context.Background()
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
	if err != nil {
		return models.ChannelMessageDelivery{}, err
	}
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("message_id = ?", message.ID).Scan(ctx))
	return delivery, nil
}

// deliver 推进一次投递所属渠道身份的管道并返回该投递的最新状态。
func (f *wechatOutboundFixture) deliver(t *testing.T, id string) models.ChannelMessageDelivery {
	t.Helper()
	advanceDelivery(t, f.db, f.worker, id)
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("id = ?", id).Scan(context.Background()))
	return delivery
}

// requireConflict 断言 err 是指定原因的业务冲突。
func requireConflict(t *testing.T, err error, reason string) {
	t.Helper()
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, reason, conflict.Reason)
}

// TestWechatOutbound 验证公众号外发：用户消息开启 48 小时 5 条的回复窗口并在收件箱摘要给出，文本与图片经客服消息接口发送，图片先上传临时素材；
// 超过文本上限、带说明、不支持类型的附件与引用在发送时拒绝；额度用尽后拒绝发送；微信判定超出回复限制时投递失败且本地窗口失效；互动事件只为已有渠道身份开启窗口。
func TestWechatOutbound(t *testing.T) {
	t.Parallel()
	f := newWechatOutboundFixture(t)
	ctx := context.Background()
	const customPath, uploadPath = "/cgi-bin/message/custom/send", "/cgi-bin/media/upload"

	// 没有渠道身份的关注事件不建立会话，也不开启窗口。
	start := time.Now().Add(-time.Hour)
	f.push(t, "o-follower", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[subscribe]]></Event>", start)
	count, err := f.db.NewSelect().TableExpr("channel_identities").Where("channel_id = ?", f.channelID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)

	// 用户消息开启回复窗口，收件箱摘要给出到期时间与剩余条数。
	f.push(t, "o-user", wechatText("2001", "你好"), start)
	conversationID := f.conversation(t, "o-user")
	window := f.replyWindow(t, conversationID)
	require.NotNil(t, window)
	require.NotNil(t, window.Remaining)
	require.Equal(t, 5, *window.Remaining)
	require.WithinDuration(t, start.Add(48*time.Hour), window.ExpiresAt, time.Second)

	// 文本经客服消息接口发送，一条消息占用一条额度。
	f.fake.set(customPath, `{"errcode":0,"errmsg":"ok"}`)
	text, err := f.sendText(t, conversationID, "您好，请问有什么可以帮您？")
	require.NoError(t, err)
	require.Equal(t, domain.ChannelDeliverySent, f.deliver(t, text.ID).Status)
	require.JSONEq(t, `{"touser":"o-user","msgtype":"text","text":{"content":"您好，请问有什么可以帮您？"}}`, f.fake.requests(customPath)[0])
	require.Equal(t, 4, *f.replyWindow(t, conversationID).Remaining)

	// 超过渠道文本上限的文本、带说明或类型不受支持的附件、引用消息在发送时拒绝。
	_, err = f.sendText(t, conversationID, strings.Repeat("😀", domain.ChannelCapabilitiesOf(domain.ChannelTypeWechatKey).TextByteLimit/4+1))
	requireConflict(t, err, servicesessionaction.ConflictReasonTextTooLong)
	sendAttachment := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer)
	_, err = sendAttachment.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), FileID: uploadedAttachment(t, f.db, f.owner, "截图.png", "image/png"), Body: "说明",
	})
	requireConflict(t, err, servicesessionaction.ConflictReasonCaptionTooLong)
	_, err = sendAttachment.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), FileID: uploadedAttachment(t, f.db, f.owner, "合同.pdf", "application/pdf"),
	})
	requireConflict(t, err, servicesessionaction.ConflictReasonChannelAttachmentUnsupported)
	var inboundID string
	require.NoError(t, f.db.NewSelect().TableExpr("messages").Column("id").Where("conversation_id = ? AND body = '你好'", conversationID).Scan(ctx, &inboundID))
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用", ReplyToMessageID: inboundID})
	requireConflict(t, err, conversationaction.ConflictReasonReplyTargetInvalid)
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	for _, message := range history.Messages {
		if message.ID == inboundID {
			require.True(t, message.ReplyUnavailable, "wechat message quotable")
		}
	}

	// 图片先上传为临时素材，再以素材编号发送图片消息。
	f.fake.set(uploadPath, `{"type":"image","media_id":"media-1","created_at":1}`)
	fileID := uploadedAttachment(t, f.db, f.owner, "截图.png", "image/png")
	message, err := sendAttachment.Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID})
	require.NoError(t, err)
	var storageKey string
	require.NoError(t, f.db.NewSelect().Table("files").Column("storage_key").Where("id = ?", fileID).Scan(ctx, &storageKey))
	f.files[storageKey] = "png-bytes"
	var image models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&image).Where("message_id = ?", message.ID).Scan(ctx))
	require.Equal(t, domain.ChannelDeliverySent, f.deliver(t, image.ID).Status)
	require.Contains(t, f.fake.requests(uploadPath)[0], "png-bytes")
	require.JSONEq(t, `{"touser":"o-user","msgtype":"image","image":{"media_id":"media-1"}}`, f.fake.requests(customPath)[1])

	// 用尽窗口额度后拒绝发送。
	f.fake.set(customPath, `{"errcode":0,"errmsg":"ok"}`)
	for range 3 {
		delivery, err := f.sendText(t, conversationID, "补充说明")
		require.NoError(t, err)
		require.Equal(t, domain.ChannelDeliverySent, f.deliver(t, delivery.ID).Status)
	}
	require.Nil(t, f.replyWindow(t, conversationID))
	_, err = f.sendText(t, conversationID, "额度已用尽")
	requireConflict(t, err, conversationaction.ConflictReasonChannelReplyWindowClosed)

	// 微信判定超出回复限制时投递失败，发送前已有的窗口全部失效：较早开启的菜单窗口先到期被选用，较晚开启的消息窗口一并失效。
	f.push(t, "o-user", wechatText("2002", "还在吗"), start.Add(time.Minute))
	f.push(t, "o-user", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[CLICK]]></Event><EventKey><![CDATA[menu-0]]></EventKey>", time.Now())
	require.Equal(t, 5, *f.replyWindow(t, conversationID).Remaining)
	f.fake.set(customPath, `{"errcode":45047,"errmsg":"out of response count limit"}`)
	late, err := f.sendText(t, conversationID, "稍后回复")
	require.NoError(t, err)
	got := f.deliver(t, late.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status)
	require.Equal(t, channeladapter.CodeReplyWindowClosed, got.LastError)
	require.Nil(t, f.replyWindow(t, conversationID))
	_, err = f.sendText(t, conversationID, "仍然关闭")
	requireConflict(t, err, conversationaction.ConflictReasonChannelReplyWindowClosed)
	f.fake.set(customPath, `{"errcode":0,"errmsg":"ok"}`)
	f.push(t, "o-user", "<MsgType><![CDATA[event]]></MsgType><Event><![CDATA[CLICK]]></Event><EventKey><![CDATA[menu-1]]></EventKey>", time.Now().Add(time.Second))
	window = f.replyWindow(t, conversationID)
	require.NotNil(t, window)
	require.Equal(t, 3, *window.Remaining)
	require.WithinDuration(t, time.Now().Add(time.Minute), window.ExpiresAt, 10*time.Second)
	_, err = f.sendText(t, conversationID, "菜单回复")
	require.NoError(t, err)
}
