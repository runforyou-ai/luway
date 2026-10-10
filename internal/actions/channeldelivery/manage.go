//go:build server

package channeldelivery

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Manager 执行企业会话投递的人工处理。
type Manager struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewManager 创建投递管理操作。
func NewManager(db *bun.DB, enqueuer servertask.TxEnqueuer) *Manager {
	return &Manager{db: db, enqueuer: enqueuer}
}

// Record 包含投递事实和当前渠道下允许的成员操作。
type Record struct {
	models.ChannelMessageDelivery `bun:",inherit"`
	CanRetry                      bool `bun:"can_retry"`
	Paused                        bool `bun:"paused"`
	PartiallySent                 bool `bun:"partially_sent"`
}

// ListForMessages 在调用方读取快照中返回渠道会话指定消息的投递状态与当前渠道下允许的成员操作。
func ListForMessages(ctx context.Context, db bun.IDB, workspaceID, conversationID string, messageIDs []string) ([]Record, error) {
	rows := make([]Record, 0)
	if len(messageIDs) == 0 {
		return rows, nil
	}
	err := db.NewSelect().Model(&rows).ColumnExpr("cmd.*").
		ColumnExpr("cmd.status IN ('failed','needs_review') AND cmd.last_error <> 'account_changed' AND ch.enabled AND ch.provider_account_id = cmd.provider_account_id AND ("+RecipientBoundCondition+") AS can_retry").
		ColumnExpr("cmd.status IN ('pending','retry_wait') AND NOT ch.enabled AS paused").
		ColumnExpr("EXISTS (SELECT 1 FROM channel_delivery_items AS sent WHERE sent.delivery_id = cmd.id AND sent.sent_at IS NOT NULL) AND EXISTS (SELECT 1 FROM channel_delivery_items AS unsent WHERE unsent.delivery_id = cmd.id AND unsent.sent_at IS NULL) AS partially_sent").
		Join("JOIN channels AS ch ON ch.id = cmd.channel_id AND ch.workspace_id = cmd.workspace_id").
		Join("JOIN channel_identities AS ci ON ci.id = cmd.channel_identity_id AND ci.workspace_id = cmd.workspace_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cmd.workspace_id AND svc.conversation_id = cmd.conversation_id").Where("cmd.workspace_id = ? AND cmd.conversation_id = ?", workspaceID, conversationID).Where("cmd.message_id IN (?)", bun.List(messageIDs)).OrderExpr("cmd.position").Scan(ctx)
	return rows, err
}

// Resolve 锁定渠道身份后重新校验投递状态，人工重试排到队尾并从第一个未发送请求项继续。
func (m *Manager) Resolve(ctx context.Context, identity *models.Identity, conversationID, deliveryID string, resolution domain.ChannelDeliveryResolution, confirmDuplicateRisk bool) error {
	return realtime.RunInTx(ctx, m.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		route, err := Prepare(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, err := chatstate.LockChannelConversation(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		delivery := &models.ChannelMessageDelivery{}
		err = tx.NewSelect().Model(delivery).Where("cmd.id = ? AND cmd.workspace_id = ? AND cmd.conversation_id = ?", deliveryID, identity.Workspace.ID, conversationID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		review := delivery.Status == domain.ChannelDeliveryNeedsReview
		if !review && delivery.Status != domain.ChannelDeliveryFailed {
			return ErrConflict
		}
		// 新写入的时刻取等待行锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		switch resolution {
		case domain.ChannelDeliveryRetry:
			if (review || delivery.LastError == "unknown_result") && !confirmDuplicateRisk {
				return ErrConflict
			}
			if delivery.LastError == "account_changed" || !route.Ready() || *route.ProviderAccountID != delivery.ProviderAccountID {
				return ErrConflict
			}
			// 待确认的请求项按未发送重发，释放其回复窗口预占。
			if err := settleReplyWindow(ctx, tx, delivery, channeladapter.OutcomeFailed); err != nil {
				return err
			}
			if err := moveToTail(ctx, tx, delivery); err != nil {
				return err
			}
			delivery.Status, delivery.AvailableAt = domain.ChannelDeliveryPending, now
			delivery.UncertainUntil, delivery.LastError = nil, ""
		case domain.ChannelDeliveryConfirmSent:
			if !review {
				return ErrConflict
			}
			// 人工确认的是当前待确认请求项，其余请求项排到队尾继续发送。
			if err := settleReplyWindow(ctx, tx, delivery, channeladapter.OutcomeSent); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model((*models.ChannelDeliveryItem)(nil)).Set("sent_at = ?", now).
				Where("delivery_id = ? AND seq = (SELECT MIN(seq) FROM channel_delivery_items WHERE delivery_id = ? AND sent_at IS NULL)", delivery.ID, delivery.ID).Exec(ctx); err != nil {
				return err
			}
			if err := completeOrContinue(ctx, tx, delivery, now); err != nil {
				return err
			}
			if delivery.Status == domain.ChannelDeliveryPending {
				if err := moveToTail(ctx, tx, delivery); err != nil {
					return err
				}
			}
			delivery.UncertainUntil = nil
			delivery.LastError = "manually_confirmed"
		case domain.ChannelDeliveryConfirmFailed:
			if !review {
				return ErrConflict
			}
			if err := settleReplyWindow(ctx, tx, delivery, channeladapter.OutcomeFailed); err != nil {
				return err
			}
			delivery.Status = domain.ChannelDeliveryFailed
		default:
			return ErrConflict
		}
		if err := saveDelivery(ctx, tx, conversation, delivery); err != nil {
			return err
		}
		if delivery.Status == domain.ChannelDeliveryPending && m.enqueuer != nil {
			if err := enqueueAdvance(ctx, tx, m.enqueuer, pipeline{WorkspaceID: delivery.WorkspaceID, ChannelID: delivery.ChannelID, ChannelType: route.ChannelType, IdentityID: delivery.ChannelIdentityID}, time.Time{}); err != nil {
				return err
			}
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "渠道消息投递已人工处理", "delivery_id", delivery.ID, "identity_id", identity.WorkspaceIdentity.ID, "resolution", resolution)
		return nil
	})
}

// moveToTail 把人工处理后继续发送的投递排到同一渠道身份管道的队尾。
func moveToTail(ctx context.Context, tx bun.Tx, delivery *models.ChannelMessageDelivery) error {
	return tx.NewSelect().TableExpr("channel_message_deliveries").ColumnExpr("MAX(position) + 1").
		Where("workspace_id = ? AND channel_id = ? AND channel_identity_id = ?", delivery.WorkspaceID, delivery.ChannelID, delivery.ChannelIdentityID).Scan(ctx, &delivery.Position)
}
