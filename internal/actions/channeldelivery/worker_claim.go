//go:build server

package channeldelivery

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

func (w *Worker) claim(ctx context.Context, input AdvanceInput) (*claimedDelivery, error) {
	var claimed *claimedDelivery
	err := realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		// 身份行统一协调消息生产、推进和人工重新排队。
		var p pipeline
		err := tx.NewSelect().TableExpr("channel_identities AS ci").
			ColumnExpr("ci.workspace_id, ci.channel_id, ch.type AS channel_type, ci.id AS identity_id").
			Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
			Where("ci.id = ? AND ci.workspace_id = ?", input.ChannelIdentityID, input.WorkspaceID).
			For("UPDATE OF ci").Scan(ctx, &p)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		for {
			delivery := &models.ChannelMessageDelivery{}
			err := tx.NewSelect().Model(delivery).
				Where("cmd.workspace_id = ? AND cmd.channel_id = ? AND cmd.channel_identity_id = ?", p.WorkspaceID, p.ChannelID, p.IdentityID).
				Where(unfinishedCondition).OrderExpr("cmd.position").Limit(1).Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			// 按渠道身份、会话、投递的顺序加锁，投递状态变化推进会话版本。
			conversation, err := chatstate.LockChannelConversation(ctx, tx, delivery.WorkspaceID, delivery.ConversationID)
			if err != nil {
				return err
			}
			if err := tx.NewSelect().Model(delivery).WherePK().Where("cmd.workspace_id = ?", delivery.WorkspaceID).For("UPDATE").Scan(ctx); err != nil {
				return err
			}
			// 到期判断与新写入的时刻取等待行锁之后的数据库时刻。
			now, err := serverstorage.ClockNow(ctx, tx)
			if err != nil {
				return err
			}
			step, err := w.process(ctx, tx, p, conversation, delivery, now, !input.Scheduled)
			if err != nil {
				return err
			}
			if step.next {
				continue
			}
			if step.ready {
				return enqueueAdvance(ctx, tx, w.enqueuer, p, time.Time{})
			}
			if !step.wakeAt.IsZero() {
				if err := enqueueAdvance(ctx, tx, w.enqueuer, p, step.wakeAt); err != nil {
					return err
				}
			}
			claimed = step.claimed
			return nil
		}
	})
	return claimed, err
}

// claimRoute 是认领时读取的渠道、对方编号与消息内容。
type claimRoute struct {
	Type              domain.ChannelType `bun:"type"`
	Enabled           bool               `bun:"enabled"`
	ProviderAccountID *string            `bun:"provider_account_id"`
	RecipientBound    bool               `bun:"recipient_bound"`
	Recipient         string             `bun:"external_id"`
	Body              string             `bun:"body"`
	MessageType       string             `bun:"message_type"`
	// 附件列只在附件消息上有值，文件列在文件已清理时为空。
	AttachmentName        *string `bun:"attachment_name"`
	AttachmentContentType *string `bun:"attachment_content_type"`
	AttachmentByteSize    *int64  `bun:"attachment_byte_size"`
	ImageWidth            *int    `bun:"image_width"`
	ImageHeight           *int    `bun:"image_height"`
	FileID                *string `bun:"file_id"`
	StorageKey            *string `bun:"storage_key"`
}

// headStep 是处理一个队头的结果；各字段都为零时管道暂停，由启用渠道时唤醒。
type headStep struct {
	// next 表示队头已结束，继续处理下一个队头。
	next bool
	// ready 表示队头可以发送而本次推进不认领，由立即推进认领。
	ready bool
	// wakeAt 非零时在该时刻再次推进。
	wakeAt time.Time
	// claimed 是本次认领的请求项，此时 wakeAt 为认领租约的到期时刻。
	claimed *claimedDelivery
}
