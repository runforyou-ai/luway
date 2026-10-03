//go:build server

package customerchat

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
	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// telegramAvatarRefreshInterval 是同一渠道身份两次头像同步之间的最短间隔。
const telegramAvatarRefreshInterval = "24 hours"

// ErrTelegramWebhookUnauthorized 表示 Telegram Webhook Secret 不匹配。
var ErrTelegramWebhookUnauthorized = errors.New("Telegram webhook unauthorized")

// TelegramWebhookInput 定义公开回调完成认证和状态更新所需字段。
type TelegramWebhookInput struct {
	Secret string
	// CustomerToken 是业务系统转发时附带的客户签名身份原文，只在网关转发接入时采用。
	CustomerToken string
	UpdateID      int64
	MyChatMember  bool
	Message       *telegram.InboundMessage
}

// ReceiveTelegramWebhookAction 认证 Telegram 回调并更新连接状态。
type ReceiveTelegramWebhookAction struct {
	db             *bun.DB
	agentScheduler conversationaction.CustomerAgentMessageScheduler
	mediaBackend   domain.FileStorageBackend
	tasks          servertask.TxEnqueuer
}

// NewReceiveTelegramWebhookAction 创建 Telegram Webhook 接收操作，mediaBackend 决定入站媒体的存储类型，tasks 在入站事务内投递媒体取回与头像同步任务。
func NewReceiveTelegramWebhookAction(db *bun.DB, agentScheduler conversationaction.CustomerAgentMessageScheduler, mediaBackend domain.FileStorageBackend, tasks servertask.TxEnqueuer) *ReceiveTelegramWebhookAction {
	return &ReceiveTelegramWebhookAction{db: db, agentScheduler: agentScheduler, mediaBackend: mediaBackend, tasks: tasks}
}

// Preflight 在读取请求体前校验渠道和当前 Secret。
func (a *ReceiveTelegramWebhookAction) Preflight(ctx context.Context, channelID, secret string) error {
	if !common.ValidUUID(channelID) {
		return channelaction.ErrNotFound
	}
	setting, err := loadActiveTelegramWebhookSetting(ctx, a.db, channelID, false)
	if err != nil {
		return err
	}
	return authorizeTelegramWebhook(setting, secret)
}

// Execute 在锁行后重新认证当前代次，并处理支持的 Update。
func (a *ReceiveTelegramWebhookAction) Execute(ctx context.Context, channelID string, input TelegramWebhookInput) error {
	if !common.ValidUUID(channelID) {
		return channelaction.ErrNotFound
	}
	var ignoredConflict, connected bool
	// 新客户首次并发入站触发渠道身份等唯一约束冲突时重试整个事务。
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, inboundMessageRetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		ignoredConflict, connected = false, false
		setting, err := loadActiveTelegramWebhookSetting(ctx, tx, channelID, true)
		if err != nil {
			return err
		}
		if err := authorizeTelegramWebhook(setting, input.Secret); err != nil {
			return err
		}
		// 网关转发附带的签名身份验签通过后作为发送者的核验身份，未附带时发送者为未核验。
		var customer *SignedCustomer
		if input.Message != nil && input.CustomerToken != "" && setting.ConnectionMode == string(domain.TelegramConnectionGateway) {
			signed, _, err := verifyCustomerToken(ctx, tx, setting.OrganizationID, input.CustomerToken)
			if err != nil {
				return err
			}
			customer = &signed
		}
		if input.Message != nil {
			channel := &servermodels.Channel{}
			if err := tx.NewSelect().Model(channel).
				Where("c.id = ?", channelID).
				Where("c.organization_id = ?", setting.OrganizationID).
				Where("c.type = ?", domain.ChannelTypeTelegram).
				Where(channelaction.AcceptsCustomersCondition("c")).
				Scan(ctx); err != nil {
				return fmt.Errorf("load Telegram webhook channel: %w", err)
			}
			displayName := input.Message.DisplayName
			platformMessage := &channelmessage.Inbound{
				AccountID: strconv.FormatInt(*setting.BotID, 10), ConversationID: strconv.FormatInt(input.Message.ChatID, 10), MessageID: strconv.FormatInt(input.Message.MessageID, 10),
			}
			if reply := input.Message.Reply; reply != nil {
				platformMessage.Reply = &channelmessage.Reply{MessageID: strconv.FormatInt(reply.MessageID, 10), Body: reply.Body, SenderName: reply.SenderName, SenderIsBot: reply.SenderIsBot}
			}
			inbound := InboundCustomerMessageInput{
				ExternalID: strconv.FormatInt(input.Message.SenderID, 10), DisplayName: &displayName,
				ChannelMessage:     platformMessage,
				SingleConversation: true, Body: input.Message.Body,
				IdempotencyKey: "chmsg:" + channelID + ":tg:" + strconv.FormatInt(*setting.BotID, 10) + ":" + strconv.FormatInt(input.Message.ChatID, 10) + ":" + strconv.FormatInt(input.Message.MessageID, 10),
				OriginatedAt:   input.Message.OriginatedAt, SourceOrder: input.Message.MessageID,
			}
			if customer != nil {
				inbound.VerifiedUserID, inbound.Email, inbound.SignedProfile = customer.UserID, customer.Email, &customer.Profile
			}
			// 媒体按企业当前存储配置建立取回中的文件记录，内容由取回任务写入。
			if media := input.Message.Media; media != nil {
				inbound.ExternalMedia = &InboundExternalMedia{
					ExternalID: media.UniqueID, FileName: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
					ImageWidth: media.Width, ImageHeight: media.Height, StorageBackend: a.mediaBackend,
				}
			}
			received, err := ReceiveInboundCustomerMessage(ctx, tx, a.tasks, channel, inbound)
			if err != nil {
				var conflict *conversationaction.ConflictError
				if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
					return err
				}
				ignoredConflict = true
			} else {
				// 仅新入站消息触发 AI 客服，回调重放不追加运行输入；取回中的媒体由取回任务在终态时调度。
				if received.Inserted && received.Attachment != nil && received.Attachment.TransferStatus == domain.MessageAttachmentTransferPending {
					if _, err := a.tasks.EnqueueIn(ctx, tx, RetrieveTelegramMediaActionName, RetrieveTelegramMediaInput{
						OrganizationID: channel.OrganizationID, ChannelID: channelID, ConversationID: received.Message.ConversationID,
						MessageID: received.Message.ID, FileID: received.Attachment.ID, BotID: *setting.BotID, TelegramFileID: input.Message.Media.FileID,
					}, servertask.EnqueueOptions{
						OrganizationID: channel.OrganizationID, Queue: "files", MaxAttempts: telegramMediaRetrieveMaxAttempts,
						IdempotencyKey: "tgmedia:" + received.Attachment.ID, TriggerType: servertask.TriggerBusiness,
					}); err != nil {
						return fmt.Errorf("enqueue Telegram media retrieval: %w", err)
					}
				} else if received.Inserted {
					if _, err := a.agentScheduler.ScheduleCustomerAuto(ctx, tx, channel.OrganizationID, received.Message.ConversationID, received.Session.ID, received.Message.ID); err != nil {
						return fmt.Errorf("schedule Telegram customer agent: %w", err)
					}
				}
				// 新入站消息在渠道身份超过同步间隔未同步头像时投递头像同步任务，并记录发起时间。
				refreshAvatar := false
				if received.Inserted {
					var identityID string
					err := tx.NewUpdate().Model((*servermodels.ContactChannelIdentity)(nil)).
						Set("avatar_checked_at = now()").
						Where("organization_id = ? AND id = ?", channel.OrganizationID, received.ChannelIdentityID).
						Where("avatar_checked_at IS NULL OR avatar_checked_at <= now() - ?::interval", telegramAvatarRefreshInterval).
						Returning("id").Scan(ctx, &identityID)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return fmt.Errorf("mark Telegram contact avatar refresh: %w", err)
					}
					refreshAvatar = identityID != ""
				}
				if refreshAvatar {
					if _, err := a.tasks.EnqueueIn(ctx, tx, channelaction.RefreshTelegramContactAvatarActionName, channelaction.RefreshTelegramContactAvatarInput{
						OrganizationID: channel.OrganizationID, ChannelID: channelID, ChannelIdentityID: received.ChannelIdentityID, SenderID: input.Message.SenderID,
					}, servertask.EnqueueOptions{
						OrganizationID: channel.OrganizationID, MaxAttempts: 1, IdempotencyKey: "tgavatar:" + received.ChannelIdentityID, TriggerType: servertask.TriggerBusiness,
					}); err != nil {
						return fmt.Errorf("enqueue Telegram contact avatar refresh: %w", err)
					}
				}
			}
		}
		connected = setting.WebhookStatus == nil || *setting.WebhookStatus != string(domain.TelegramWebhookStatusNormal)
		return nil
	})
	if err != nil {
		return err
	}
	// 回调首次成功时在事务之外标记 Webhook 正常，Secret 已更换时不写入。
	if connected {
		if _, err := a.db.NewUpdate().Model((*servermodels.TelegramChannelSetting)(nil)).
			Set("webhook_status = ?", domain.TelegramWebhookStatusNormal).
			Set("webhook_connected_at = now()").
			Set("updated_at = now()").
			Where("channel_id = ? AND webhook_secret = ?", channelID, input.Secret).
			Where("webhook_status IS DISTINCT FROM ?", domain.TelegramWebhookStatusNormal).
			Exec(ctx); err != nil {
			slog.Warn("标记 Telegram Webhook 正常失败", "channel_id", channelID, "error", err)
		}
	}
	attributes := []any{"channel_id", channelID, "update_id", input.UpdateID}
	if input.Message != nil {
		attributes = append(attributes, "chat_id", input.Message.ChatID, "message_id", input.Message.MessageID)
	}
	if ignoredConflict {
		slog.Warn("Telegram 消息幂等冲突已忽略", attributes...)
		return nil
	}
	slog.Info("Telegram Webhook 回调已接收", attributes...)
	return nil
}

// loadActiveTelegramWebhookSetting 读取接待客户的渠道当前可接收回调的设置。
//
// 所属企业由本次查询结果确定，调用方随后按 setting.OrganizationID 限定企业。
func loadActiveTelegramWebhookSetting(ctx context.Context, db bun.IDB, channelID string, lock bool) (*servermodels.TelegramChannelSetting, error) {
	setting := &servermodels.TelegramChannelSetting{}
	query := db.NewSelect().
		Model(setting).
		Join("JOIN channels AS c ON c.id = tcs.channel_id AND c.organization_id = tcs.organization_id").
		Where("tcs.channel_id = ?", channelID).
		Where("c.type = ?", domain.ChannelTypeTelegram).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Where("tcs.webhook_secret IS NOT NULL").
		Where("tcs.bot_id IS NOT NULL")
	if lock {
		query = query.For("SHARE OF tcs")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, channelaction.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get active Telegram webhook: %w", err)
	}
	return setting, nil
}

// authorizeTelegramWebhook 使用常量时间比较当前 Secret。
func authorizeTelegramWebhook(setting *servermodels.TelegramChannelSetting, secret string) error {
	if setting.WebhookSecret == nil {
		return ErrTelegramWebhookUnauthorized
	}
	expected := sha256.Sum256([]byte(*setting.WebhookSecret))
	provided := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		return ErrTelegramWebhookUnauthorized
	}
	return nil
}
