//go:build server

package customerdelivery

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const sendTimeout = 20 * time.Second

// mediaSendTimeout 按 50 MiB 渠道上限与约 2 Mbps 上行带宽设定。
const mediaSendTimeout = 5 * time.Minute

// leaseMargin 是认领租约在发送超时之外保留的结果保存时间。
const leaseMargin = 25 * time.Second
const uncertaintyWindow = 30 * time.Second

// ContentOpener 按文件记录中的存储类型打开附件内容。
type ContentOpener interface {
	Open(context.Context, *models.File) (io.ReadCloser, error)
}

// Worker 按渠道身份队头投递消息并恢复中断的发送。
type Worker struct {
	db       *bun.DB
	sender   telegram.Sender
	files    ContentOpener
	enqueuer servertask.Enqueuer
}

// claimedDelivery 保存认领成功后本次发送所需的凭据、目标和消息内容。
type claimedDelivery struct {
	delivery  *models.CustomerMessageDelivery
	token     string
	recipient string
	body      string
	// attachment 在附件消息上有值，文本消息为空。
	attachment *claimedAttachment
}

// claimedAttachment 保存附件消息的展示元数据与存储位置，附件记录缺失或文件已清理时 file 为空。
type claimedAttachment struct {
	name        string
	contentType string
	byteSize    int64
	imageWidth  int
	imageHeight int
	file        *models.File
}

// NewWorker 创建持久投递执行器。
func NewWorker(db *bun.DB, sender telegram.Sender, files ContentOpener, enqueuer servertask.Enqueuer) *Worker {
	return &Worker{db: db, sender: sender, files: files, enqueuer: enqueuer}
}

// Scan 为到期队头补充幂等唤醒，HTTP 发送由独立任务执行。
func (w *Worker) Scan(ctx context.Context, _ struct{}) error {
	var ids []string
	if err := readyHeads(w.db).Limit(100).Scan(ctx, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := w.enqueuer.Enqueue(ctx, SendActionName, Input{DeliveryID: id}, servertask.EnqueueOptions{Queue: servertask.QueueDelivery, IdempotencyKey: "cdeliv-item:" + id}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("客户消息投递扫描失败", "delivery_id", id, "error", err)
		}
	}
	return nil
}

// readyHeads 构造到期队头查询：各渠道身份管道中最小的非终态投递，且已到可发送、租约过期或待确认到期时间；按最早更新排序。
func readyHeads(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("customer_message_deliveries AS d").Column("d.id").
		Join("JOIN channels AS ch ON ch.id = d.channel_id AND ch.organization_id = d.organization_id").
		Where("(d.status IN ('pending','retry_wait') AND d.available_at <= now()) OR (d.status = 'sending' AND d.lease_expires_at <= now()) OR (d.status = 'uncertain' AND d.uncertain_until <= now())").
		Where("d.status IN ('sending', 'uncertain') OR (ch.enabled AND NOT EXISTS (SELECT 1 FROM customer_channel_send_gates AS gate WHERE gate.organization_id = d.organization_id AND gate.channel_id = d.channel_id AND gate.flood_wait_until > now()))").
		Where("NOT EXISTS (SELECT 1 FROM customer_message_deliveries AS earlier WHERE earlier.organization_id = d.organization_id AND earlier.channel_id = d.channel_id AND earlier.contact_channel_identity_id = d.contact_channel_identity_id AND earlier.position < d.position AND earlier.status IN ('pending','retry_wait','sending','uncertain'))").
		OrderExpr("d.updated_at, d.id")
}

// Execute 认领投递并在短事务之外调用 Telegram；本次完成发送时为同一渠道身份的下一条到期投递创建一次发送任务，其余情况由扫描唤醒。
//
// 同一渠道身份按位置串行发送，不同渠道身份并行发送；所属企业由投递记录确定，认领之后的查询都按该企业限定。
func (w *Worker) Execute(ctx context.Context, input Input) error {
	var identityID string
	err := w.db.NewSelect().Model((*models.CustomerMessageDelivery)(nil)).Column("contact_channel_identity_id").Where("id = ?", input.DeliveryID).Scan(ctx, &identityID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	sent, err := w.send(ctx, input.DeliveryID)
	if err != nil || !sent {
		return err
	}
	// 唤醒任务不设幂等键，与仍在运行的同一投递任务并存。
	var nextID string
	err = readyHeads(w.db).Where("d.contact_channel_identity_id = ?", identityID).Limit(1).Scan(ctx, &nextID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		_, err = w.enqueuer.Enqueue(ctx, SendActionName, Input{DeliveryID: nextID}, servertask.EnqueueOptions{Queue: servertask.QueueDelivery})
	}
	if err != nil {
		slog.Warn("唤醒渠道身份下一条客户消息投递失败", "contact_channel_identity_id", identityID, "error", err)
	}
	return nil
}

// send 认领投递、调用 Telegram 并保存结果，返回本次是否认领并完成发送。
func (w *Worker) send(ctx context.Context, deliveryID string) (bool, error) {
	claimed, err := w.claim(ctx, deliveryID)
	if err != nil || claimed == nil {
		return false, err
	}
	delivery, recipient := claimed.delivery, claimed.recipient
	var messageID int64
	var sendErr error
	if attachment := claimed.attachment; attachment == nil {
		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		messageID, sendErr = w.sender.SendText(sendCtx, claimed.token, telegram.TextMessage{ChatID: recipient, Body: claimed.body, ReplyMessageID: delivery.ReplyProviderMessageID})
		cancel()
	} else {
		sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
		// 附件记录缺失、文件已清理或存储读取失败时平台未收到请求，投递直接失败并可人工重试。
		sendErr = &telegram.SendError{Code: "attachment_unavailable"}
		if attachment.file != nil {
			content, openErr := w.files.Open(sendCtx, attachment.file)
			if openErr != nil {
				slog.Warn("客户消息附件内容读取失败", "delivery_id", delivery.ID, "file_id", attachment.file.ID, "error", openErr)
			} else {
				messageID, sendErr = w.sender.SendMedia(sendCtx, claimed.token, telegram.MediaMessage{
					ChatID: recipient, FileName: attachment.name, ContentType: attachment.contentType, ByteSize: attachment.byteSize,
					ImageWidth: attachment.imageWidth, ImageHeight: attachment.imageHeight,
					Content: content, Caption: claimed.body, ReplyMessageID: delivery.ReplyProviderMessageID,
				})
				content.Close()
			}
		}
		cancel()
	}
	// 请求结束或服务关闭后使用独立上下文保存平台结果。
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	return true, w.finish(saveCtx, delivery, recipient, messageID, sendErr)
}

// claim 只认领身份管道的最小非终态投递。
func (w *Worker) claim(ctx context.Context, id string) (*claimedDelivery, error) {
	var claimed *claimedDelivery
	err := realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		delivery := &models.CustomerMessageDelivery{}
		if err := tx.NewSelect().Model(delivery).Where("cmd.id = ?", id).Scan(ctx); err != nil {
			return err
		}
		// 身份行统一协调消息生产、扫描和人工重新排队。
		if _, err := tx.ExecContext(ctx, "SELECT id FROM contact_channel_identities WHERE id = ? AND organization_id = ? FOR UPDATE", delivery.ContactChannelIdentityID, delivery.OrganizationID); err != nil {
			return err
		}
		// 按渠道身份、会话、投递的顺序加锁，投递状态变化推进会话版本。
		conversation, err := chatstate.LockChannelConversation(ctx, tx, delivery.OrganizationID, delivery.ConversationID)
		if err != nil {
			return err
		}
		if err := tx.NewSelect().Model(delivery).WherePK().Where("cmd.organization_id = ?", delivery.OrganizationID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		now := time.Now().UTC()
		switch delivery.Status {
		case domain.CustomerDeliverySending:
			if delivery.LeaseExpiresAt != nil && !delivery.LeaseExpiresAt.After(now) {
				until := now.Add(uncertaintyWindow)
				delivery.Status, delivery.UncertainUntil, delivery.LastError = domain.CustomerDeliveryUncertain, &until, "unknown_result"
				slog.Warn("客户消息发送认领过期，等待人工确认", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
				delivery.LeaseWorker, delivery.LeaseExpiresAt = nil, nil
				return saveDelivery(ctx, tx, conversation, delivery)
			}
			return nil
		case domain.CustomerDeliveryUncertain:
			if delivery.UncertainUntil != nil && !delivery.UncertainUntil.After(now) {
				delivery.Status = domain.CustomerDeliveryNeedsReview
				return saveDelivery(ctx, tx, conversation, delivery)
			}
			return nil
		case domain.CustomerDeliveryPending, domain.CustomerDeliveryRetryWait:
			if delivery.AvailableAt.After(now) {
				return nil
			}
		default:
			return nil
		}
		blocked, err := tx.NewSelect().TableExpr("customer_message_deliveries").
			Where("organization_id = ? AND channel_id = ? AND contact_channel_identity_id = ? AND position < ?", delivery.OrganizationID, delivery.ChannelID, delivery.ContactChannelIdentityID, delivery.Position).
			Where("status IN ('pending','retry_wait','sending','uncertain')").Exists(ctx)
		if err != nil || blocked {
			return err
		}
		var route struct {
			Enabled   bool    `bun:"enabled"`
			BotID     *int64  `bun:"bot_id"`
			Token     *string `bun:"bot_token"`
			Recipient string  `bun:"external_id"`
			Body      string  `bun:"body"`
			Type      string  `bun:"type"`
			// 附件列只在附件消息上有值，文件列在文件已清理时为空。
			AttachmentName        *string `bun:"attachment_name"`
			AttachmentContentType *string `bun:"attachment_content_type"`
			AttachmentByteSize    *int64  `bun:"attachment_byte_size"`
			ImageWidth            *int    `bun:"image_width"`
			ImageHeight           *int    `bun:"image_height"`
			FileID                *string `bun:"file_id"`
			StorageBackend        *string `bun:"storage_backend"`
			StorageKey            *string `bun:"storage_key"`
		}
		if err := tx.NewSelect().TableExpr("channels AS ch").ColumnExpr("ch.enabled, tcs.bot_id, tcs.bot_token, cci.external_id, msg.body, msg.type").
			ColumnExpr("ma.name AS attachment_name, ma.content_type AS attachment_content_type, ma.byte_size AS attachment_byte_size, ma.image_width, ma.image_height, f.id AS file_id, f.storage_backend, f.storage_key").
			Join("JOIN telegram_channel_settings AS tcs ON tcs.channel_id = ch.id AND tcs.organization_id = ch.organization_id").
			Join("JOIN contact_channel_identities AS cci ON cci.channel_id = ch.id AND cci.organization_id = ch.organization_id AND cci.id = ?", delivery.ContactChannelIdentityID).
			Join("JOIN messages AS msg ON msg.id = ? AND msg.organization_id = ch.organization_id AND msg.conversation_id = ?", delivery.MessageID, delivery.ConversationID).
			Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.organization_id = msg.organization_id").
			Join("LEFT JOIN files AS f ON f.id = ma.file_id AND f.organization_id = ma.organization_id").
			Where("ch.id = ? AND ch.organization_id = ? AND ch.type = ?", delivery.ChannelID, delivery.OrganizationID, domain.ChannelTypeTelegram).Scan(ctx, &route); err != nil {
			return err
		}
		if route.BotID == nil || *route.BotID != delivery.BotID {
			delivery.Status, delivery.LastError = domain.CustomerDeliveryFailed, "bot_changed"
			slog.Warn("客户消息因机器人变化停止投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
			return saveDelivery(ctx, tx, conversation, delivery)
		}
		if !route.Enabled {
			slog.Info("客户消息因渠道停用暂停投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
			return nil
		}
		if route.Token == nil || *route.Token == "" {
			delivery.Status, delivery.LastError = domain.CustomerDeliveryFailed, "invalid_token"
			slog.Warn("客户消息因缺少机器人凭据停止投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
			return saveDelivery(ctx, tx, conversation, delivery)
		}
		blocked, err = tx.NewSelect().TableExpr("customer_channel_send_gates").Where("organization_id = ? AND channel_id = ? AND flood_wait_until > now()", delivery.OrganizationID, delivery.ChannelID).Exists(ctx)
		if err != nil || blocked {
			return err
		}
		// 租约覆盖本次消息类型的发送超时。
		timeout := sendTimeout
		if route.Type == string(domain.MessageTypeAttachment) {
			timeout = mediaSendTimeout
		}
		worker, expires := uuid.NewV7().String(), now.Add(timeout+leaseMargin)
		delivery.Status, delivery.LeaseWorker, delivery.LeaseExpiresAt = domain.CustomerDeliverySending, &worker, &expires
		delivery.Attempt++
		delivery.LastError = ""
		if err := saveDelivery(ctx, tx, conversation, delivery); err != nil {
			return err
		}
		claimed = &claimedDelivery{delivery: delivery, token: *route.Token, recipient: route.Recipient, body: route.Body}
		// 附件消息始终按附件投递，附件记录或文件缺失时不携带存储位置。
		if route.Type == string(domain.MessageTypeAttachment) {
			claimed.attachment = &claimedAttachment{}
			if route.AttachmentName != nil && route.FileID != nil {
				claimed.attachment = &claimedAttachment{
					name: *route.AttachmentName, contentType: *route.AttachmentContentType, byteSize: *route.AttachmentByteSize,
					imageWidth: *route.ImageWidth, imageHeight: *route.ImageHeight,
					file: &models.File{ID: *route.FileID, OrganizationID: delivery.OrganizationID, StorageBackend: *route.StorageBackend, StorageKey: *route.StorageKey},
				}
			}
		}
		return nil
	})
	return claimed, err
}

// finish 保存带认领标识的平台结果，未知结果绝不自动重发。
func (w *Worker) finish(ctx context.Context, delivery *models.CustomerMessageDelivery, recipient string, messageID int64, sendErr error) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		// 与入站共用渠道身份锁，使映射写入和迟到引用关联串行提交。
		if _, err := tx.ExecContext(ctx, "SELECT id FROM contact_channel_identities WHERE id = ? AND organization_id = ? FOR UPDATE", delivery.ContactChannelIdentityID, delivery.OrganizationID); err != nil {
			return err
		}
		conversation, err := chatstate.LockChannelConversation(ctx, tx, delivery.OrganizationID, delivery.ConversationID)
		if err != nil {
			return err
		}
		worker := delivery.LeaseWorker
		current := &models.CustomerMessageDelivery{}
		if err := tx.NewSelect().Model(current).Where("cmd.organization_id = ? AND cmd.id = ?", delivery.OrganizationID, delivery.ID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if current.Status != domain.CustomerDeliverySending || current.LeaseWorker == nil || worker == nil || *current.LeaseWorker != *worker {
			return nil
		}
		now := time.Now().UTC()
		current.LeaseWorker, current.LeaseExpiresAt = nil, nil
		current.Status = domain.CustomerDeliverySent
		if sendErr == nil && messageID > 0 {
			current.ProviderMessageID, current.SentAt = &messageID, &now
			// 使用本次实际发送的聊天目标建立平台映射。
			if err := channelmessage.Record(ctx, tx, &models.ChannelMessage{
				MessageID: current.MessageID, OrganizationID: current.OrganizationID, ConversationID: current.ConversationID,
				ChannelID: current.ChannelID, ProviderAccountID: strconv.FormatInt(current.BotID, 10), ProviderConversationID: recipient, ProviderMessageID: strconv.FormatInt(messageID, 10),
			}); err != nil {
				return err
			}
		} else {
			var failure *telegram.SendError
			if !errors.As(sendErr, &failure) {
				failure = &telegram.SendError{Code: "unknown_result"}
			}
			current.LastError = failure.Code
			switch failure.Code {
			case "rate_limited":
				current.Status, current.AvailableAt = domain.CustomerDeliveryRetryWait, now.Add(failure.RetryAfter)
				if _, err := tx.ExecContext(ctx, `INSERT INTO customer_channel_send_gates (channel_id, organization_id, flood_wait_until)
     VALUES (?, ?, ?) ON CONFLICT (channel_id) DO UPDATE SET flood_wait_until = GREATEST(customer_channel_send_gates.flood_wait_until, EXCLUDED.flood_wait_until), updated_at = now()`, current.ChannelID, current.OrganizationID, current.AvailableAt); err != nil {
					return err
				}
			case "invalid_token", "invalid_recipient", "invalid_message", "recipient_unavailable", "message_rejected", "attachment_unavailable":
				current.Status = domain.CustomerDeliveryFailed
			default:
				until := now.Add(uncertaintyWindow)
				current.Status, current.UncertainUntil = domain.CustomerDeliveryUncertain, &until
			}
		}
		if err := saveDelivery(ctx, tx, conversation, current); err != nil {
			return err
		}
		slog.Info("客户消息投递结果已保存", "delivery_id", current.ID, "channel_id", current.ChannelID, "status", current.Status, "attempt", current.Attempt, "error_code", current.LastError)
		return nil
	})
}

// saveDelivery 在持有会话锁的事务内更新投递状态与运行字段，并推进会话版本。
func saveDelivery(ctx context.Context, db bun.IDB, conversation *models.Conversation, delivery *models.CustomerMessageDelivery) error {
	delivery.UpdatedAt = time.Now().UTC()
	if _, err := db.NewUpdate().Model(delivery).WherePK().Where("organization_id = ?", delivery.OrganizationID).Exec(ctx); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeTimeline)
}
