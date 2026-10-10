//go:build server

package channeldelivery

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// leaseMargin 是认领租约在发送超时之外保留的结果保存时间。
const leaseMargin = 25 * time.Second

// uncertaintyWindow 是认领过期后等待平台结果的时长，到期转为待人工确认。
const uncertaintyWindow = 30 * time.Second

// ContentOpener 按文件记录中的存储类型打开附件内容。
type ContentOpener interface {
	Open(context.Context, *models.File) (io.ReadCloser, error)
}

// Worker 按渠道身份管道逐个请求项投递消息并恢复中断的发送。
type Worker struct {
	db       *bun.DB
	adapters *channeladapter.Registry
	files    ContentOpener
	enqueuer servertask.TxEnqueuer
}

// claimedDelivery 保存认领成功后本次发送的管道、投递、请求项、平台目标与内容。
type claimedDelivery struct {
	pipeline pipeline
	delivery *models.ChannelMessageDelivery
	item     *models.ChannelDeliveryItem
	outbound channeladapter.Outbound
	timeout  time.Duration
	target   channeladapter.Target
	request  channeladapter.Request
	// attachment 在附件请求项上有值，附件记录缺失或文件已清理时 file 为空。
	attachment *claimedAttachment
}

// claimedAttachment 保存附件请求项的展示元数据与存储位置。
type claimedAttachment struct {
	meta channeladapter.Attachment
	file *models.File
}

// NewWorker 创建持久投递执行器。
func NewWorker(db *bun.DB, adapters *channeladapter.Registry, files ContentOpener, enqueuer servertask.TxEnqueuer) *Worker {
	return &Worker{db: db, adapters: adapters, files: files, enqueuer: enqueuer}
}

// Advance 推进渠道身份的投递管道：依次处理已到期的队头，队头需要等待时在到期时刻排下一次推进；认领到请求项时在短事务之外调用平台并保存结果，
// 每次最多调用一次平台，定时推进不调用平台。同一渠道身份按位置串行发送，不同渠道身份并行发送。
func (w *Worker) Advance(ctx context.Context, input AdvanceInput) error {
	claimed, err := w.claim(ctx, input)
	if err != nil || claimed == nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, claimed.timeout)
	// 附件记录缺失、文件已清理或存储读取失败时平台未收到请求，投递直接失败并可人工重试。
	result := channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "attachment_unavailable"}
	if attachment := claimed.attachment; attachment == nil {
		result = claimed.outbound.Send(sendCtx, claimed.target, claimed.request)
	} else if attachment.file != nil {
		content, openErr := w.files.Open(sendCtx, attachment.file)
		if openErr != nil {
			slog.WarnContext(ctx, "渠道消息附件内容读取失败", "delivery_id", claimed.delivery.ID, "file_id", attachment.file.ID, "error", openErr)
		} else {
			file := attachment.meta
			file.Content = content
			claimed.request.File = &file
			result = claimed.outbound.Send(sendCtx, claimed.target, claimed.request)
			content.Close()
		}
	}
	cancel()
	// 请求结束或服务关闭后使用独立上下文保存平台结果。
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	return w.finish(saveCtx, claimed, result)
}

// claim 锁定渠道身份后依次处理队头：已结束的队头继续处理下一个，需要等待的队头在同一事务中按到期时刻排下一次推进，可发送的队头认领其下一个请求项；
// 定时推进遇到可发送的队头时排一次立即推进，由立即推进认领。
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
	StorageBackend        *string `bun:"storage_backend"`
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

// process 在持有渠道身份、会话与投递行锁的事务中处理一个队头，send 为假时不认领可发送的队头。
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
		ColumnExpr("ma.name AS attachment_name, ma.content_type AS attachment_content_type, ma.byte_size AS attachment_byte_size, ma.image_width, ma.image_height, f.id AS file_id, f.storage_backend, f.storage_key").
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
				file: &models.File{ID: *route.FileID, WorkspaceID: delivery.WorkspaceID, StorageBackend: *route.StorageBackend, StorageKey: *route.StorageKey},
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

// finish 保存带认领标识的平台结果，并在渠道身份仍有未完成投递时排一次立即推进；未知结果绝不自动重发。
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
		if err := channelmessage.Record(ctx, tx, &models.ChannelMessage{
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
