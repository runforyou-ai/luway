//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AuthorizationMessagePath 是授权接入公众号消息与事件接收地址在部署地址下的路径，$APPID$ 由微信替换为公众号 AppID。
const AuthorizationMessagePath = "/api/public/wechat/accounts/$APPID$/messages"

// KeyMessagePath 返回密钥接入渠道的服务器地址在部署地址下的路径。
func KeyMessagePath(channelID string) string {
	return "/api/public/wechat/channels/" + channelID + "/messages"
}

// ReceiveMessageAction 接收公众号推送到消息接收地址的用户消息，转换为渠道入站事件后写入任务队列。
type ReceiveMessageAction struct {
	db    *bun.DB
	tasks servertask.Enqueuer
}

// NewReceiveMessageAction 创建公众号消息接收操作。
func NewReceiveMessageAction(db *bun.DB, tasks servertask.Enqueuer) *ReceiveMessageAction {
	return &ReceiveMessageAction{db: db, tasks: tasks}
}

// keyReceiver 是密钥接入渠道接收推送所需的凭据与接待状态。
type keyReceiver struct {
	servermodels.WechatChannelKey `bun:",extend"`
	AppID                         string `bun:"app_id"`
	Accepts                       bool   `bun:"accepts"`
}

// loadKeyReceiver 读取已连接公众号的密钥接入渠道的凭据与接待状态，渠道不存在或尚未连接时返回 channelaction.ErrNotFound。
func loadKeyReceiver(ctx context.Context, db bun.IDB, channelID string) (*keyReceiver, error) {
	if !str.IsUUID(channelID) {
		return nil, channelaction.ErrNotFound
	}
	receiver := &keyReceiver{}
	err := db.NewSelect().Model(receiver).ColumnExpr("wck.*").
		ColumnExpr("c.provider_account_id AS app_id").
		ColumnExpr("("+channelaction.AcceptsCustomersCondition("c")+") AS accepts").
		Join("JOIN channels AS c ON c.id = wck.channel_id AND c.workspace_id = wck.workspace_id").
		Where("wck.channel_id = ? AND c.type = ?", channelID, domain.ChannelTypeWechatKey).
		Where("c.provider_account_id IS NOT NULL").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat key receiver: %w", err)
	}
	return receiver, nil
}

// VerifyKeyServer 校验微信验证服务器地址的请求签名，通过后记录密钥接入渠道按当前 Token 验证成功的时间。渠道不存在或尚未连接时返回 channelaction.ErrNotFound，校验失败时返回 ErrPushUnauthorized。
func (a *ReceiveMessageAction) VerifyKeyServer(ctx context.Context, channelID string, query wechat.PushQuery) error {
	receiver, err := loadKeyReceiver(ctx, a.db, channelID)
	if err != nil {
		return err
	}
	// 推送时间戳由微信生成，按本机时钟校验时效。
	if err := wechat.VerifySignature(receiver.Token, query, time.Now()); err != nil { //clock:local
		return fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	// verified 记录本次是否写入验证时间，事务提交后记录日志。
	var verified bool
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model((*servermodels.WechatChannelKey)(nil)).
			Set("server_verified_at = now()").
			Where("channel_id = ? AND token = ?", channelID, receiver.Token).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save wechat server verification: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("save wechat server verification: %w", err)
		}
		if rows > 0 {
			realtime.Notify(ctx, realtime.ServiceInboxChannelChanged(receiver.WorkspaceID, channelID))
			verified = true
		}
		return nil
	})
	if err == nil && verified {
		slog.InfoContext(logscope.WithWorkspace(ctx, receiver.WorkspaceID), "公众号服务器地址验证成功", "channel_id", channelID)
	}
	return err
}

// ReceiveKeyMessage 按密钥接入渠道的 Token 与消息加密方式校验并解密推送，渠道接待客户时把用户消息写入入站事件任务。渠道不存在或尚未连接时返回 channelaction.ErrNotFound，校验失败时返回 ErrPushUnauthorized。
func (a *ReceiveMessageAction) ReceiveKeyMessage(ctx context.Context, channelID string, query wechat.PushQuery, body []byte) error {
	receiver, err := loadKeyReceiver(ctx, a.db, channelID)
	if err != nil {
		return err
	}
	// 推送时间戳由微信生成，按本机时钟校验时效；安全模式解密后的接收方须为本渠道公众号。
	now := time.Now() //clock:local
	plain := body
	if domain.WechatEncryptionMode(receiver.EncryptionMode) == domain.WechatEncryptionSafe {
		var cipher *wechat.Cipher
		if cipher, err = wechat.NewCipher(receiver.Token, receiver.EncodingAESKey, receiver.AppID); err != nil {
			return err
		}
		plain, err = cipher.Open(query, body, now)
	} else {
		err = wechat.VerifySignature(receiver.Token, query, now)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	message, err := wechat.ParseMessage(plain)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	event, ok := inboundEvent(message)
	if !ok || !receiver.Accepts {
		return nil
	}
	return channelinbound.EnqueueEvent(ctx, a.tasks, channelinbound.ReceiveEventInput{
		WorkspaceID: receiver.WorkspaceID, ChannelID: channelID, AccountID: receiver.AppID, Event: event,
	}, "")
}

// ReceiveAuthorizationMessage 按第三方平台的 Token 与 EncodingAESKey 校验并解密推送，公众号 appID 的授权接入渠道授权有效、消息接收方为该公众号且渠道接待客户时把用户消息写入入站事件任务；
// 全网发布检测专用测试公众号的消息交给检测应答。返回 nil 时按约定回复 success，返回非 nil 正文时以它作为响应（客服消息检测为空正文）。尚未配置平台时返回 ErrPlatformNotConfigured，校验失败时返回 ErrPushUnauthorized。
func (a *ReceiveMessageAction) ReceiveAuthorizationMessage(ctx context.Context, appID string, query wechat.PushQuery, body []byte) ([]byte, error) {
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if err != nil {
		return nil, err
	}
	cipher, err := wechat.NewCipher(platform.Token, platform.EncodingAESKey, platform.ComponentAppID)
	if err != nil {
		return nil, err
	}
	// 推送时间戳由微信生成，按本机时钟校验时效。
	plain, err := cipher.Open(query, body, time.Now()) //clock:local
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	message, err := wechat.ParseMessage(plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	if wechat.IsReleaseTestAccount(appID, message.ToUserName) {
		return a.answerReleaseTest(ctx, cipher, appID, message)
	}
	event, ok := inboundEvent(message)
	if !ok {
		return nil, nil
	}
	// 只接收当前平台下授权有效、接收方为该公众号原始 ID 且接待客户的渠道的消息。
	var channel struct {
		ID          string `bun:"id"`
		WorkspaceID string `bun:"workspace_id"`
	}
	err = a.db.NewSelect().TableExpr("channels AS c").Column("c.id", "c.workspace_id").
		Join("JOIN wechat_authorizations AS wa ON wa.channel_id = c.id AND wa.workspace_id = c.workspace_id").
		Where("c.type = ? AND c.provider_account_id = ?", domain.ChannelTypeWechatAuthorization, appID).
		Where("wa.revoked_at IS NULL AND wa.component_app_id = ?", platform.ComponentAppID).
		Where("wa.user_name = ?", message.ToUserName).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Scan(ctx, &channel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat authorization receiver: %w", err)
	}
	return nil, channelinbound.EnqueueEvent(ctx, a.tasks, channelinbound.ReceiveEventInput{
		WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, AccountID: appID, Event: event,
	}, "")
}

// 公众号回复窗口的触发动作，互动事件的 Action 取后三者。
const (
	triggerMessage   = "message"
	triggerSubscribe = "subscribe"
	triggerScan      = "scan"
	triggerMenu      = "menu"
)

// interactionActions 是开启回复窗口的互动事件对应的触发动作：关注、扫描带参数二维码与点击推事件或扫码推事件菜单。
var interactionActions = map[string]string{
	wechat.EventSubscribe: triggerSubscribe, wechat.EventScan: triggerScan,
	wechat.EventClick: triggerMenu, wechat.EventScanCodePush: triggerMenu, wechat.EventScanCodeWaitMsg: triggerMenu,
}

// inboundEvent 把用户消息与互动事件转换为渠道入站事件：文本、位置与链接转为文本，图片、语音与视频转为媒体引用，关注、扫码与菜单点击转为互动；其他事件与消息类型返回 false。
func inboundEvent(message wechat.Message) (channeladapter.InboundEvent, bool) {
	event := channeladapter.InboundEvent{
		Kind: channeladapter.InboundEventMessage, ID: message.MsgID, Sender: message.FromUserName, ChatID: message.FromUserName,
		OccurredAt: time.Unix(message.CreateTime, 0),
	}
	if message.MsgType == wechat.MessageTypeEvent {
		action, ok := interactionActions[message.Event]
		if !ok {
			return channeladapter.InboundEvent{}, false
		}
		// 事件没有消息编号，以发送方、发生时间、事件类型与事件键标识。
		event.Kind, event.Action = channeladapter.InboundEventInteraction, action
		event.ID = "event:" + message.FromUserName + ":" + strconv.FormatInt(message.CreateTime, 10) + ":" + message.Event + ":" + message.EventKey
		return event, true
	}
	if message.MsgID == "" {
		return channeladapter.InboundEvent{}, false
	}
	switch message.MsgType {
	case wechat.MessageTypeText:
		event.Text = message.Content
	case wechat.MessageTypeLocation:
		event.Text = message.LocationX + ", " + message.LocationY
		if message.Label != "" {
			event.Text = message.Label + " (" + event.Text + ")"
		}
	case wechat.MessageTypeLink:
		event.Text = strings.Join(arr.Filter([]string{message.Title, message.Description, message.URL}, func(line string) bool { return line != "" }), "\n")
	case wechat.MessageTypeImage:
		event.Media = []channeladapter.InboundMedia{{Ref: message.MediaID, FileName: "image"}}
	case wechat.MessageTypeVoice:
		// 语音按微信给出的音频格式命名，未给出格式时由取回内容识别类型。
		media := channeladapter.InboundMedia{Ref: message.MediaID, FileName: "voice"}
		if format := strings.ToLower(message.Format); format != "" {
			media.FileName, media.ContentType = "voice."+format, "audio/"+format
		}
		event.Media = []channeladapter.InboundMedia{media}
	case wechat.MessageTypeVideo, wechat.MessageTypeShortVideo:
		event.Media = []channeladapter.InboundMedia{{Ref: message.MediaID, FileName: "video.mp4", ContentType: "video/mp4"}}
	default:
		return channeladapter.InboundEvent{}, false
	}
	if event.Text == "" && (len(event.Media) == 0 || event.Media[0].Ref == "") {
		return channeladapter.InboundEvent{}, false
	}
	return event, true
}
