//go:build server

package telegram

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrWebhookUnauthorized 表示 Telegram Webhook Secret 不匹配。
var ErrWebhookUnauthorized = errors.New("Telegram webhook unauthorized")

// UpdateInput 是一次 Webhook 回调的认证信息与已解析的 Update。
type UpdateInput struct {
	Secret string
	// CustomerToken 是业务系统转发时附带的客户签名身份原文，只在网关转发接入时采用。
	CustomerToken string
	Update        telegram.Update
}

// ReceiveUpdateAction 认证 Telegram Webhook 回调，把私聊消息转换为渠道入站事件写入任务队列，并在回调首次成功时标记 Webhook 正常。
type ReceiveUpdateAction struct {
	db    *bun.DB
	tasks servertask.Enqueuer
}

// NewReceiveUpdateAction 创建 Telegram Webhook 接收操作。
func NewReceiveUpdateAction(db *bun.DB, tasks servertask.Enqueuer) *ReceiveUpdateAction {
	return &ReceiveUpdateAction{db: db, tasks: tasks}
}

// webhookReceiver 是接待客户的 Telegram 渠道接收回调所需的设置与当前连接的机器人编号。
type webhookReceiver struct {
	servermodels.TelegramChannelSetting `bun:",extend"`
	AccountID                           string `bun:"provider_account_id"`
}

// Preflight 在读取请求体前校验渠道和当前 Secret。
func (a *ReceiveUpdateAction) Preflight(ctx context.Context, channelID, secret string) error {
	_, err := a.authorize(ctx, channelID, secret)
	return err
}

// Execute 认证回调后把私聊消息写入入站事件任务；网关转发接入时校验附带的签名身份，作为发送者的核验结论随事件写入。渠道不存在或不接待客户时返回 channelaction.ErrNotFound，Secret 不匹配时返回 ErrWebhookUnauthorized，签名身份无效时返回 ErrCustomerIdentityInvalid。
func (a *ReceiveUpdateAction) Execute(ctx context.Context, channelID string, input UpdateInput) error {
	receiver, err := a.authorize(ctx, channelID, input.Secret)
	if err != nil {
		return err
	}
	if message := input.Update.Message; message != nil {
		event := channelinbound.ReceiveEventInput{
			WorkspaceID: receiver.WorkspaceID, ChannelID: channelID, AccountID: receiver.AccountID, Event: inboundEvent(message),
		}
		// 业务系统转发的请求自身给出核验结论，未附带签名身份时发送者为未核验；直接连接时核验由渠道身份断言维护。
		if receiver.ConnectionMode == string(domain.TelegramConnectionGateway) {
			event.Assertion = &channelinbound.Assertion{}
			if input.CustomerToken != "" {
				signed, _, err := customerchataction.VerifyCustomerToken(ctx, a.db, receiver.WorkspaceID, input.CustomerToken)
				if err != nil {
					return err
				}
				event.Assertion = &channelinbound.Assertion{UserID: signed.UserID, Email: signed.Email, Profile: &signed.Profile}
			}
		}
		if err := channelinbound.EnqueueEvent(ctx, a.tasks, event, ""); err != nil {
			return fmt.Errorf("enqueue Telegram inbound event: %w", err)
		}
	}
	// 回调首次成功时标记 Webhook 正常并通知成员，Secret 已更换时不写入。
	if receiver.WebhookStatus != nil && *receiver.WebhookStatus == string(domain.TelegramWebhookStatusNormal) {
		return nil
	}
	result, err := a.db.NewUpdate().Model((*servermodels.TelegramChannelSetting)(nil)).
		Set("webhook_status = ?", domain.TelegramWebhookStatusNormal).
		Set("webhook_connected_at = now()").
		Where("channel_id = ? AND webhook_secret = ?", channelID, input.Secret).
		Where("webhook_status IS DISTINCT FROM ?", domain.TelegramWebhookStatusNormal).
		Exec(ctx)
	if err != nil {
		slog.WarnContext(ctx, "标记 Telegram Webhook 正常失败", "channel_id", channelID, "error", err)
	} else if rows, _ := result.RowsAffected(); rows > 0 {
		realtime.Publish(realtime.ServiceInboxChannelChanged(receiver.WorkspaceID, channelID))
	}
	return nil
}

// authorize 读取接待客户的渠道当前可接收回调的设置，并以常量时间比较 Secret。
func (a *ReceiveUpdateAction) authorize(ctx context.Context, channelID, secret string) (*webhookReceiver, error) {
	if !str.IsUUID(channelID) {
		return nil, channelaction.ErrNotFound
	}
	receiver := &webhookReceiver{}
	err := a.db.NewSelect().Model(receiver).
		ColumnExpr("tcs.*, c.provider_account_id").
		Join("JOIN channels AS c ON c.id = tcs.channel_id AND c.workspace_id = tcs.workspace_id").
		Where("tcs.channel_id = ? AND c.type = ?", channelID, domain.ChannelTypeTelegram).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Where("tcs.webhook_secret IS NOT NULL AND c.provider_account_id IS NOT NULL").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get Telegram webhook receiver: %w", err)
	}
	expected := sha256.Sum256([]byte(*receiver.WebhookSecret))
	provided := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		return nil, ErrWebhookUnauthorized
	}
	return receiver, nil
}

// inboundEvent 把 Telegram 私聊消息转换为渠道入站事件：事件编号取聊天与消息编号，媒体说明随媒体写入。
func inboundEvent(message *telegram.InboundMessage) channeladapter.InboundEvent {
	chatID, messageID := strconv.FormatInt(message.ChatID, 10), strconv.FormatInt(message.MessageID, 10)
	event := channeladapter.InboundEvent{
		Kind: channeladapter.InboundEventMessage, ID: chatID + ":" + messageID,
		Sender: strconv.FormatInt(message.SenderID, 10), SenderName: message.DisplayName, ChatID: chatID, MessageID: messageID,
		OccurredAt: message.OriginatedAt, Unsupported: message.Unsupported,
	}
	if reply := message.Reply; reply != nil {
		event.Reply = &channeladapter.InboundReply{MessageID: strconv.FormatInt(reply.MessageID, 10), Body: reply.Body, SenderName: reply.SenderName, SenderIsBot: reply.SenderIsBot}
	}
	if media := message.Media; media != nil {
		event.Media = []channeladapter.InboundMedia{{
			Ref: media.FileID, FileName: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
			Width: media.Width, Height: media.Height, Caption: message.Body,
		}}
	} else {
		event.Text = message.Body
	}
	return event
}
