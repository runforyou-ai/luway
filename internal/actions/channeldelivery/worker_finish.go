//go:build server

package channeldelivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

func (w *Worker) finish(ctx context.Context, claimed *claimedDelivery, result channeladapter.Result) error {
	delivery := claimed.delivery
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		// 与入站共用渠道身份锁，使映射写入和迟到引用关联串行提交。
		if _, err := tx.ExecContext(ctx, "SELECT id FROM channel_identities WHERE id = ? AND workspace_id = ? FOR UPDATE", delivery.ChannelIdentityID, delivery.WorkspaceID); err != nil {
			return err
		}
		conversation, err := chatstate.LockChannelConversation(ctx, tx, delivery.WorkspaceID, delivery.ConversationID)
		if err != nil {
			return err
		}
		worker := delivery.LeaseWorker
		current := &models.ChannelMessageDelivery{}
		if err := tx.NewSelect().Model(current).Where("cmd.workspace_id = ? AND cmd.id = ?", delivery.WorkspaceID, delivery.ID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if current.Status != domain.ChannelDeliverySending || current.LeaseWorker == nil || worker == nil || *current.LeaseWorker != *worker {
			return nil
		}
		// 平台判定回复窗口已关闭时，认领本次发送前已开启的窗口立即到期，发送期间新互动开启的窗口保留；认领与开启窗口都在持有渠道身份锁后取实际时刻。
		if result.Code == channeladapter.CodeReplyWindowClosed {
			if _, err := tx.ExecContext(ctx, `UPDATE channel_reply_windows SET expires_at = LEAST(expires_at, now())
     WHERE workspace_id = ? AND channel_identity_id = ? AND created_at <= ?`,
				current.WorkspaceID, current.ChannelIdentityID, current.ClaimedAt); err != nil {
				return err
			}
		}
		if err := settleReplyWindow(ctx, tx, current, result.Outcome); err != nil {
			return err
		}
		// 新写入的时刻取等待行锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		current.LeaseWorker, current.LeaseExpiresAt = nil, nil
		current.LastError = result.Code
		switch result.Outcome {
		case channeladapter.OutcomeSent:
			if err := w.recordSentItem(ctx, tx, current, claimed, result.ProviderMessageID, now); err != nil {
				return err
			}
		case channeladapter.OutcomeRetry:
			current.Status, current.AvailableAt = domain.ChannelDeliveryRetryWait, now.Add(result.RetryAfter)
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_send_gates (channel_id, workspace_id, flood_wait_until)
     VALUES (?, ?, ?) ON CONFLICT (channel_id) DO UPDATE SET flood_wait_until = GREATEST(channel_send_gates.flood_wait_until, EXCLUDED.flood_wait_until)`, current.ChannelID, current.WorkspaceID, current.AvailableAt); err != nil {
				return err
			}
		case channeladapter.OutcomeFailed:
			current.Status = domain.ChannelDeliveryFailed
		default:
			until := now.Add(uncertaintyWindow)
			current.Status, current.UncertainUntil = domain.ChannelDeliveryUncertain, &until
			if current.LastError == "" {
				current.LastError = "unknown_result"
			}
		}
		if err := saveDelivery(ctx, tx, conversation, current); err != nil {
			return err
		}
		unfinished, err := tx.NewSelect().TableExpr("channel_message_deliveries").
			Where("workspace_id = ? AND channel_id = ? AND channel_identity_id = ?", current.WorkspaceID, current.ChannelID, current.ChannelIdentityID).
			Where(unfinishedCondition).Exists(ctx)
		if err != nil {
			return err
		}
		if unfinished {
			if err := enqueueAdvance(ctx, tx, w.enqueuer, claimed.pipeline, time.Time{}); err != nil {
				return err
			}
		}
		slog.InfoContext(ctx, "渠道消息投递结果已保存", "delivery_id", current.ID, "channel_id", current.ChannelID, "item_seq", claimed.item.Seq, "status", current.Status, "attempt", current.Attempt, "error_code", current.LastError)
		return nil
	})
}

// recordSentItem 记录请求项已发送并按请求项序号建立平台消息映射；仍有未发送请求项时投递回到待发送。
func (w *Worker) recordSentItem(ctx context.Context, tx bun.Tx, delivery *models.ChannelMessageDelivery, claimed *claimedDelivery, providerMessageID string, now time.Time) error {
	item := claimed.item
	item.SentAt, item.ProviderMessageID = &now, support.NilIfZero(providerMessageID)
	if _, err := tx.NewUpdate().Model(item).Column("provider_message_id", "sent_at").WherePK().Exec(ctx); err != nil {
		return err
	}
	// 使用本次实际发送的平台账号与对方编号建立平台映射。
	if providerMessageID != "" {
		if err := RecordMapping(ctx, tx, &models.ChannelMessage{
			MessageID: delivery.MessageID, Part: item.Seq, WorkspaceID: delivery.WorkspaceID, ConversationID: delivery.ConversationID,
			ChannelID: delivery.ChannelID, ProviderAccountID: delivery.ProviderAccountID, ProviderConversationID: claimed.target.Recipient, ProviderMessageID: providerMessageID,
		}); err != nil {
			return err
		}
	}
	return completeOrContinue(ctx, tx, delivery, now)
}

// completeOrContinue 在全部请求项已发送时把投递记为已发送，否则让投递回到待发送以继续下一个请求项。
func completeOrContinue(ctx context.Context, db bun.IDB, delivery *models.ChannelMessageDelivery, now time.Time) error {
	remaining, err := db.NewSelect().Model((*models.ChannelDeliveryItem)(nil)).Where("delivery_id = ? AND sent_at IS NULL", delivery.ID).Exists(ctx)
	if err != nil {
		return err
	}
	if remaining {
		delivery.Status, delivery.AvailableAt = domain.ChannelDeliveryPending, now
		return nil
	}
	delivery.Status, delivery.SentAt = domain.ChannelDeliverySent, &now
	return nil
}

// saveDelivery 在持有会话锁的事务内保存投递状态与运行字段，并推进会话版本。
func saveDelivery(ctx context.Context, db bun.IDB, conversation *models.Conversation, delivery *models.ChannelMessageDelivery) error {
	if _, err := db.NewUpdate().Model(delivery).WherePK().Where("workspace_id = ?", delivery.WorkspaceID).Exec(ctx); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeTimeline)
}
