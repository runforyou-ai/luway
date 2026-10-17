//go:build server

package channeldelivery

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

func (w *Worker) process(ctx context.Context, tx bun.Tx, p pipeline, conversation *models.Conversation, delivery *models.ChannelMessageDelivery, now time.Time, send bool) (headStep, error) {
	switch delivery.Status {
	case domain.ChannelDeliverySending:
		if delivery.LeaseExpiresAt != nil && delivery.LeaseExpiresAt.After(now) {
			return headStep{wakeAt: *delivery.LeaseExpiresAt}, nil
		}
		until := now.Add(uncertaintyWindow)
		delivery.Status, delivery.UncertainUntil, delivery.LastError = domain.ChannelDeliveryUncertain, &until, "unknown_result"
		slog.WarnContext(ctx, "渠道消息发送认领过期，等待人工确认", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
		delivery.LeaseWorker, delivery.LeaseExpiresAt = nil, nil
		return headStep{wakeAt: until}, saveDelivery(ctx, tx, conversation, delivery)
	case domain.ChannelDeliveryUncertain:
		if delivery.UncertainUntil != nil && delivery.UncertainUntil.After(now) {
			return headStep{wakeAt: *delivery.UncertainUntil}, nil
		}
		delivery.Status = domain.ChannelDeliveryNeedsReview
		return headStep{next: true}, saveDelivery(ctx, tx, conversation, delivery)
	case domain.ChannelDeliveryPending, domain.ChannelDeliveryRetryWait:
	default:
		// 加锁前已被其他事务结束的队头直接跳过。
		return headStep{next: true}, nil
	}
	if delivery.AvailableAt.After(now) {
		return headStep{wakeAt: delivery.AvailableAt}, nil
	}
	// fail 把队头记为失败并继续处理下一个队头。
	fail := func(code string) (headStep, error) {
		delivery.Status, delivery.LastError = domain.ChannelDeliveryFailed, code
		return headStep{next: true}, saveDelivery(ctx, tx, conversation, delivery)
	}
	var route claimRoute
	if err := tx.NewSelect().TableExpr("channels AS ch").ColumnExpr("ch.type, ch.enabled, ch.provider_account_id, ci.external_id, msg.body, msg.type AS message_type").
		ColumnExpr("("+RecipientBoundCondition+") AS recipient_bound").
		ColumnExpr("ma.name AS attachment_name, ma.content_type AS attachment_content_type, ma.byte_size AS attachment_byte_size, ma.image_width, ma.image_height, f.id AS file_id, f.storage_key").
		Join("JOIN channel_identities AS ci ON ci.channel_id = ch.id AND ci.workspace_id = ch.workspace_id AND ci.id = ?", delivery.ChannelIdentityID).
		Join("JOIN messages AS msg ON msg.id = ? AND msg.workspace_id = ch.workspace_id AND msg.conversation_id = ?", delivery.MessageID, delivery.ConversationID).
		Join("JOIN service_conversations AS svc ON svc.workspace_id = msg.workspace_id AND svc.conversation_id = msg.conversation_id").
		Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.workspace_id = msg.workspace_id").
		Join("LEFT JOIN files AS f ON f.id = ma.file_id AND f.workspace_id = ma.workspace_id").
		Where("ch.id = ? AND ch.workspace_id = ?", delivery.ChannelID, delivery.WorkspaceID).Scan(ctx, &route); err != nil {
		return headStep{}, err
	}
	if route.ProviderAccountID == nil || *route.ProviderAccountID != delivery.ProviderAccountID {
		slog.WarnContext(ctx, "渠道消息因平台账号变化停止投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
		return fail("account_changed")
	}
	// 对方已不是会话发起人（解除或更换了绑定）时记为失败，会话内容只发给发起人。
	if !route.RecipientBound {
		slog.WarnContext(ctx, "渠道消息因对方解除绑定停止投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
		return fail("recipient_unbound")
	}
	if !route.Enabled {
		slog.InfoContext(ctx, "渠道消息因渠道停用暂停投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID)
		return headStep{}, nil
	}
	outbound, ok := channeladapter.Lookup[channeladapter.Outbound](w.adapters, route.Type)
	if !ok {
		slog.WarnContext(ctx, "渠道消息因渠道不支持外发停止投递", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID, "channel_type", route.Type)
		return fail("outbound_unsupported")
	}
	var gate time.Time
	err := tx.NewSelect().TableExpr("channel_send_gates").Column("flood_wait_until").
		Where("workspace_id = ? AND channel_id = ? AND flood_wait_until > ?", delivery.WorkspaceID, delivery.ChannelID, now).Scan(ctx, &gate)
	if err == nil {
		return headStep{wakeAt: gate}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return headStep{}, err
	}
	if !send {
		return headStep{ready: true}, nil
	}
	attachment := route.MessageType == string(domain.MessageTypeAttachment)
	item, err := nextItem(ctx, tx, delivery, outbound, route.Body, attachment)
	if err != nil {
		return headStep{}, err
	}
	// 回复窗口渠道为全部未发送请求项预占同一窗口的额度，已预占的窗口到期时释放后重新预占，额度不足时剩余请求项不发送。
	if domain.ChannelCapabilitiesOf(route.Type).ReplyWindow {
		if delivery.ReplyWindowID != nil {
			reserved := &models.ChannelReplyWindow{}
			if err := tx.NewSelect().Model(reserved).Where("crw.id = ? AND crw.workspace_id = ?", *delivery.ReplyWindowID, delivery.WorkspaceID).Scan(ctx); err != nil {
				return headStep{}, err
			}
			if !reserved.ExpiresAt.After(now) {
				if err := settleReplyWindow(ctx, tx, delivery, channeladapter.OutcomeFailed); err != nil {
					return headStep{}, err
				}
			}
		}
		if delivery.ReplyWindowID == nil {
			var unsent int
			if err := tx.NewSelect().Model((*models.ChannelDeliveryItem)(nil)).ColumnExpr("count(*)").Where("delivery_id = ? AND sent_at IS NULL", delivery.ID).Scan(ctx, &unsent); err != nil {
				return headStep{}, err
			}
			window, err := reserveReplyWindow(ctx, tx, delivery.WorkspaceID, delivery.ChannelIdentityID, unsent)
			if err != nil {
				return headStep{}, err
			}
			if window == nil {
				return fail(channeladapter.CodeReplyWindowClosed)
			}
			delivery.ReplyWindowID = &window.ID
		}
	}
	timeout := outbound.SendTimeout(channeladapter.Item{Body: item.Body, Attachment: item.Attachment})
	worker, expires := uuid.NewV7().String(), now.Add(timeout+leaseMargin)
	delivery.Status, delivery.LeaseWorker, delivery.LeaseExpiresAt, delivery.ClaimedAt = domain.ChannelDeliverySending, &worker, &expires, &now
	delivery.Attempt++
	delivery.LastError = ""
	if err := saveDelivery(ctx, tx, conversation, delivery); err != nil {
		return headStep{}, err
	}
	result := &claimedDelivery{
		pipeline: p, delivery: delivery, item: item, outbound: outbound, timeout: timeout,
		target: channeladapter.Target{WorkspaceID: delivery.WorkspaceID, ChannelID: delivery.ChannelID, AccountID: delivery.ProviderAccountID, Recipient: route.Recipient},
		request: channeladapter.Request{
			Item:             channeladapter.Item{Body: item.Body, Attachment: item.Attachment},
			ReplyToMessageID: delivery.ReplyProviderMessageID,
		},
	}
	// 附件请求项始终按附件投递，附件记录或文件缺失时不携带存储位置。
	if item.Attachment {
		result.attachment = &claimedAttachment{}
		if route.AttachmentName != nil && route.FileID != nil {
			result.attachment = &claimedAttachment{
				meta: channeladapter.Attachment{
					Name: *route.AttachmentName, ContentType: *route.AttachmentContentType, ByteSize: *route.AttachmentByteSize,
					ImageWidth: *route.ImageWidth, ImageHeight: *route.ImageHeight,
				},
				file: &models.File{ID: *route.FileID, WorkspaceID: delivery.WorkspaceID, StorageKey: *route.StorageKey},
			}
		}
	}
	return headStep{wakeAt: expires, claimed: result}, nil
}

// nextItem 返回投递的第一个未发送请求项，首次认领时按渠道适配器拆分并保存全部请求项。
func nextItem(ctx context.Context, tx bun.Tx, delivery *models.ChannelMessageDelivery, outbound channeladapter.Outbound, body string, attachment bool) (*models.ChannelDeliveryItem, error) {
	exists, err := tx.NewSelect().Model((*models.ChannelDeliveryItem)(nil)).Where("delivery_id = ?", delivery.ID).Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		planned := outbound.Plan(body, attachment)
		items := make([]models.ChannelDeliveryItem, len(planned))
		for index, item := range planned {
			items[index] = models.ChannelDeliveryItem{DeliveryID: delivery.ID, Seq: index + 1, WorkspaceID: delivery.WorkspaceID, Body: item.Body, Attachment: item.Attachment}
		}
		if _, err := tx.NewInsert().Model(&items).ExcludeColumn("created_at", "updated_at").Exec(ctx); err != nil {
			return nil, err
		}
	}
	item := &models.ChannelDeliveryItem{}
	if err := tx.NewSelect().Model(item).Where("cdi.delivery_id = ? AND cdi.sent_at IS NULL", delivery.ID).OrderExpr("cdi.seq").Limit(1).Scan(ctx); err != nil {
		return nil, err
	}
	return item, nil
}
