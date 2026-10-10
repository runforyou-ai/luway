//go:build server

package customernotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

const (
	// ScanActionName 扫描到达邮件通知检查时间的客户会话。
	ScanActionName = "customer_notify.scan"
	// NotifyActionName 检查单个客户会话的未读真人回复并发送邮件通知。
	NotifyActionName = "customer_notify.email"
	// ScheduleKey 是邮件通知扫描的定时计划键。
	ScheduleKey = "customer-email-notify-scan"
	// ResumeTokenTTL 是邮件中回访令牌的有效期。
	ResumeTokenTTL = 30 * 24 * time.Hour
)

const (
	// notifyDelay 是第一条未通知的真人回复到邮件通知检查的等待时长。
	notifyDelay = 3 * time.Minute
	// notifyMaxAttempts 是单个会话邮件通知任务的最大尝试次数；耗尽后检查时间仍在，由下次扫描重新投递。
	notifyMaxAttempts = 3
	// scanLimit 是单次扫描投递的最大会话数。
	scanLimit = 100
)

// NotifyInput 定义一次客户会话邮件通知检查。
type NotifyInput struct {
	WorkspaceID    string `bun:"workspace_id" json:"workspaceId"`
	ConversationID string `bun:"conversation_id" json:"conversationId"`
}

// ScheduleCheck 在真人对客回复的事务中登记邮件通知检查时间；已有待检查时间时保持不变，由第一条未通知的回复起算。调用方持有会话锁。
func ScheduleCheck(ctx context.Context, db bun.IDB, workspaceID, conversationID string, repliedAt time.Time) error {
	if _, err := db.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
		Set("contact_notify_due_at = COALESCE(contact_notify_due_at, ?)", repliedAt.Add(notifyDelay)).
		Where("workspace_id = ? AND conversation_id = ?", workspaceID, conversationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("schedule customer email notification: %w", err)
	}
	return nil
}

// Worker 发送客服回复的邮件通知。
type Worker struct {
	db        *bun.DB
	enqueuer  servertask.Enqueuer
	sender    Sender
	publicURL func() string
}

// NewWorker 创建邮件通知 Worker；publicURL 返回生成回访链接的部署地址。
func NewWorker(db *bun.DB, enqueuer servertask.Enqueuer, sender Sender, publicURL func() string) *Worker {
	return &Worker{db: db, enqueuer: enqueuer, sender: sender, publicURL: publicURL}
}

// Scan 为正常状态工作区中到达检查时间的客户会话投递通知任务，同一会话同时只有一个活动任务；部署未配置邮件发送时不投递，待发送的通知保留到配置后发送。
func (w *Worker) Scan(ctx context.Context, _ struct{}) error {
	if !w.sender.Enabled() {
		return nil
	}
	var rows []NotifyInput
	if err := w.db.NewSelect().Model((*servermodels.ChannelConversation)(nil)).
		Column("cc.workspace_id", "cc.conversation_id").
		Where("cc.contact_notify_due_at <= now()").
		Where(identityaction.ActiveWorkspaceCondition("cc.workspace_id")).
		OrderExpr("cc.contact_notify_due_at ASC").
		Limit(scanLimit).
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("scan customer email notifications: %w", err)
	}
	enqueued := 0
	for _, row := range rows {
		if err := w.enqueuer.Enqueue(ctx, NotifyActionName, row, servertask.EnqueueOptions{
			WorkspaceID: row.WorkspaceID, MaxAttempts: notifyMaxAttempts, IdempotencyKey: "customer-email-notify:" + row.ConversationID,
		}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(logscope.WithWorkspace(ctx, row.WorkspaceID), "投递客户邮件通知任务失败", "conversation_id", row.ConversationID, "error", err)
			continue
		}
		enqueued++
	}
	if enqueued > 0 {
		slog.InfoContext(ctx, "已投递客户邮件通知任务", "count", enqueued)
	}
	return nil
}

// pendingReply 是一条等待邮件通知的真人对客回复。
type pendingReply struct {
	MessageSeq       int64   `bun:"message_seq"`
	ServiceSessionID string  `bun:"service_session_id"`
	Body             string  `bun:"body"`
	SenderName       string  `bun:"sender_name"`
	AttachmentName   *string `bun:"attachment_name"`
}

// Execute 取晚于客户已读位置与已通知位置的真人回复合并发送一封邮件，发信在事务外执行；成功后在会话锁内推进已通知位置、写入成员可见事件，并按锁内剩余回复重新计时或清除检查时间；非网站渠道或联系人没有邮箱时清除检查时间；发信失败时保留检查时间与水位，按任务重试；部署未配置邮件发送时保留检查时间，留给配置后的扫描。
func (w *Worker) Execute(ctx context.Context, input NotifyInput) error {
	if !w.sender.Enabled() {
		return nil
	}
	customer := &servermodels.ChannelConversation{}
	err := w.db.NewSelect().Model(customer).
		Where("cc.workspace_id = ? AND cc.conversation_id = ?", input.WorkspaceID, input.ConversationID).
		Where("cc.contact_notify_due_at <= now()").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load customer notification state: %w", err)
	}
	recipient, err := loadCustomerRecipient(ctx, w.db, input.WorkspaceID, input.ConversationID)
	if err != nil {
		return err
	}
	if domain.ChannelType(recipient.ChannelType) != domain.ChannelTypeWebsite || recipient.Email == nil {
		return w.clear(ctx, input)
	}
	replies, err := w.pendingReplies(ctx, input)
	if err != nil {
		return err
	}
	if len(replies) == 0 {
		return w.settle(ctx, input, nil, "")
	}
	message, err := w.composeMessage(ctx, input, recipient, replies)
	if err != nil {
		return err
	}
	if err := w.sender.Send(ctx, message); err != nil {
		return err
	}
	return w.settle(ctx, input, &replies[len(replies)-1], *recipient.Email)
}

// pendingReplies 读取晚于客户已读位置与已通知位置的真人对客回复。
func (w *Worker) pendingReplies(ctx context.Context, input NotifyInput) ([]pendingReply, error) {
	var replies []pendingReply
	if err := humanRepliesQuery(w.db, input.WorkspaceID, input.ConversationID).
		ColumnExpr("msg.message_seq, msg.service_session_id, msg.body, oi.display_name AS sender_name, ma.name AS attachment_name").
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = msg.workspace_id AND cc.conversation_id = msg.conversation_id").
		Join("LEFT JOIN message_attachments AS ma ON ma.workspace_id = msg.workspace_id AND ma.message_id = msg.id").
		Where("msg.message_seq > GREATEST(cc.contact_read_seq, cc.contact_notified_seq)").
		OrderExpr("msg.message_seq").
		Scan(ctx, &replies); err != nil {
		return nil, fmt.Errorf("load pending customer replies: %w", err)
	}
	return replies, nil
}

// composeMessage 按客户语言生成邮件；匿名访客附带回访链接，签名身份访客提示登录企业网站继续对话。
func (w *Worker) composeMessage(ctx context.Context, input NotifyInput, recipient customerRecipient, replies []pendingReply) (mail.Message, error) {
	workspace := &servermodels.Workspace{}
	if err := w.db.NewSelect().Model(workspace).Column("o.name").
		Where("o.id = ?", input.WorkspaceID).Scan(ctx); err != nil {
		return mail.Message{}, fmt.Errorf("load notification workspace: %w", err)
	}
	channel, err := serviceroute.LoadConversationChannel(ctx, w.db, input.WorkspaceID, input.ConversationID)
	if err != nil {
		return mail.Message{}, err
	}
	locale, err := translationaction.CustomerLocale(ctx, w.db, input.WorkspaceID, input.ConversationID, domain.CustomerLocale(channel.DefaultLocale))
	if err != nil {
		return mail.Message{}, err
	}
	content := notificationContent{Locale: locale, Workspace: workspace.Name, Recipient: *recipient.Email,
		Replies: arr.Map(replies, func(reply pendingReply) notificationReply {
			return notificationReply{SenderName: reply.SenderName, Body: reply.Body, AttachmentName: reply.AttachmentName}
		})}
	// 匿名访客凭回访令牌回到原会话，签名身份访客由企业网站恢复登录身份。
	if kind, _, _ := customeridentity.ParseExternalID(recipient.ExternalID); kind == customeridentity.ExternalIDAnonymous {
		token, err := w.issueResumeToken(ctx, input, recipient.ChannelIdentityID)
		if err != nil {
			return mail.Message{}, err
		}
		content.ResumeURL = strings.TrimRight(w.publicURL(), "/") + "/chat/" + channel.ID + "?resume=" + token
	}
	return renderNotification(content)
}

// clear 清除客户会话的邮件通知检查时间，用于无法接收邮件的会话与重试耗尽的检查。
func (w *Worker) clear(ctx context.Context, input NotifyInput) error {
	if _, err := w.db.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
		Set("contact_notify_due_at = NULL").
		Where("workspace_id = ? AND conversation_id = ?", input.WorkspaceID, input.ConversationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("clear customer email notification: %w", err)
	}
	return nil
}

// FinalizeFailure 在邮件通知任务重试耗尽后清除检查时间，放弃本批回复，由下一条真人回复重新计时。
func (w *Worker) FinalizeFailure(ctx context.Context, input NotifyInput, taskErr error) error {
	slog.WarnContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "客户邮件通知重试耗尽，放弃本批回复",
		"conversation_id", input.ConversationID, "error", taskErr)
	return w.clear(ctx, input)
}

// settle 在会话锁内收尾一次检查：发出邮件时推进已通知位置并写入只对成员可见的通知事件；锁内仍有晚于已通知位置与已读位置的真人回复时从现在重新计时，否则清除检查时间。last 为空表示本次没有发信。
func (w *Worker) settle(ctx context.Context, input NotifyInput, last *pendingReply, address string) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, input.WorkspaceID, input.ConversationID)
		if err != nil {
			return err
		}
		var notifiedSeq int64
		if last != nil {
			notifiedSeq = last.MessageSeq
		}
		// 回复写入持有同一会话锁，锁内判断的剩余回复包含检查期间新到的回复。
		remaining := humanRepliesQuery(tx, input.WorkspaceID, input.ConversationID).ColumnExpr("1").
			Where("msg.message_seq > GREATEST(?, cc.contact_notified_seq, cc.contact_read_seq)", notifiedSeq)
		if _, err := tx.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
			Set("contact_notified_seq = GREATEST(contact_notified_seq, ?)", notifiedSeq).
			Set("contact_notify_due_at = CASE WHEN EXISTS (?) THEN now() + make_interval(secs => ?) ELSE NULL END", remaining, notifyDelay.Seconds()).
			Where("workspace_id = ? AND conversation_id = ?", input.WorkspaceID, input.ConversationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("settle customer email notification: %w", err)
		}
		if last == nil {
			return nil
		}
		payload, err := json.Marshal(domain.ServiceSessionEmailEvent{ServiceSessionID: last.ServiceSessionID, Email: address})
		if err != nil {
			return fmt.Errorf("encode email notified event: %w", err)
		}
		eventType := string(domain.ConversationSystemEventServiceSessionEmailNotified)
		if _, _, err := chatstate.AppendSystemEvent(ctx, tx, conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: input.WorkspaceID, ConversationID: input.ConversationID,
			ServiceSessionID: &last.ServiceSessionID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
			SystemEventType: &eventType, SystemEventPayload: payload,
		}); err != nil {
			return fmt.Errorf("append email notified event: %w", err)
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "客服回复已通过邮件通知客户",
			"conversation_id", input.ConversationID, "last_message_seq", last.MessageSeq)
		return nil
	})
}

// issueResumeToken 签发绑定渠道身份与客户会话的回访令牌，只保存令牌摘要。
func (w *Worker) issueResumeToken(ctx context.Context, input NotifyInput, channelIdentityID string) (string, error) {
	token := random.Hex(32)
	if _, err := w.db.NewInsert().Model(&servermodels.ConversationResumeToken{
		ID: uuid.NewV7().String(), WorkspaceID: input.WorkspaceID, ConversationID: input.ConversationID,
		ChannelIdentityID: channelIdentityID, TokenHash: random.HashToken(token),
	}).Value("expires_at", "now() + make_interval(secs => ?)", ResumeTokenTTL.Seconds()).Exec(ctx); err != nil {
		return "", fmt.Errorf("save resume token: %w", err)
	}
	return token, nil
}
