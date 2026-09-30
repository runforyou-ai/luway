//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ReturnedHandoffActionName 为退回队列的 AI 员工周期分配承接成员并按承接结果通知客户。
const ReturnedHandoffActionName = "agent.customer_returned_handoff"

// returnedHandoffMaxAttempts 是转人工承接任务的最大尝试次数；执行时在锁内重新判断，对客通知按幂等键只写一次。
const returnedHandoffMaxAttempts = 3

// ReturnedHandoffInput 定义退回队列的 AI 员工周期的转人工承接任务。
type ReturnedHandoffInput struct {
	OrganizationID   string `json:"organizationId"`
	ServiceSessionID string `json:"serviceSessionId"`
	AgentIdentityID  string `json:"agentIdentityId"` // 对客通知的发送者，即被退回的 AI 员工。
	NoticeKey        string `json:"noticeKey"`
}

// enqueueReturnedHandoff 在调用方事务中投递转人工承接任务。
func enqueueReturnedHandoff(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, input ReturnedHandoffInput) error {
	if _, err := enqueuer.EnqueueIn(ctx, db, ReturnedHandoffActionName, input, servertask.EnqueueOptions{MaxAttempts: returnedHandoffMaxAttempts}); err != nil {
		return fmt.Errorf("enqueue returned service session handoff: %w", err)
	}
	return nil
}

// customerHandoffNotice 按客户语言、承接结果与企业工作时间生成转人工对客话术：已有承接成员时介绍该成员，否则按是否处于工作时间告知排队或下次处理时间，访客可接收邮件通知时追加请其留下邮箱；客户语言不在对客语言中时使用渠道默认接待语言。
func customerHandoffNotice(ctx context.Context, db bun.IDB, emailSender customernotify.Sender, channel *servermodels.Channel, conversationID string, assigneeName *string, now time.Time) (string, error) {
	locale, err := translationaction.CustomerLocale(ctx, db, channel.OrganizationID, conversationID, domain.CustomerLocale(channel.DefaultLocale))
	if err != nil {
		return "", err
	}
	if assigneeName != nil {
		return i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffAssigned, map[string]any{"Name": *assigneeName}), nil
	}
	notice, err := queuedHandoffNotice(ctx, db, channel.OrganizationID, locale, now)
	if err != nil {
		return "", err
	}
	requested, err := customernotify.EmailRequested(ctx, db, emailSender, channel.OrganizationID, conversationID)
	if err != nil || !requested {
		return notice, err
	}
	return notice + "\n\n" + i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffEmailRequest, nil), nil
}

// queuedHandoffNotice 生成留在队列的转人工话术：工作时间内告知排队，非工作时间告知下次处理时间。
func queuedHandoffNotice(ctx context.Context, db bun.IDB, organizationID string, locale domain.CustomerLocale, now time.Time) (string, error) {
	hours, err := customerserviceaction.LoadBusinessHours(ctx, db, organizationID)
	if err != nil {
		return "", err
	}
	if hours.Open(now) {
		return i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffQueued, nil), nil
	}
	next, ok := hours.NextOpening(now)
	if !ok {
		return i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffAfterHoursUnscheduled, nil), nil
	}
	local := next.In(hours.Location())
	// UTC 偏移写作 GMT+8、GMT+5:30，零偏移写作 GMT。
	_, seconds := local.Zone()
	offset, sign := "GMT", "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	if seconds > 0 {
		offset += fmt.Sprintf("%s%d", sign, seconds/3600)
		if minutes := seconds % 3600 / 60; minutes > 0 {
			offset += fmt.Sprintf(":%02d", minutes)
		}
	}
	return i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffAfterHours, map[string]any{
		"Month": int(local.Month()), "MonthName": local.Format("Jan"), "Day": local.Day(), "Clock": local.Format("15:04"), "Offset": offset,
	}), nil
}

// HandOffReturnedSession 为退回队列的 AI 员工周期完成转人工承接：先锁定可分配成员再锁会话，周期仍在原队列且无人负责时分配，渠道来源随后以原 AI 员工身份按承接结果通知客户。
// 周期已关闭、已换代或已发出该通知时直接结束；周期已由 AI 员工负责时不通知。
func (a *ExecuteAction) HandOffReturnedSession(ctx context.Context, input ReturnedHandoffInput) error {
	queued := &servermodels.ServiceSession{}
	err := a.db.NewSelect().Model(queued).Column("ss.id", "ss.conversation_id", "ss.status", "ss.team_id", "ss.assignee_identity_id").
		Where("ss.organization_id = ? AND ss.id = ?", input.OrganizationID, input.ServiceSessionID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load returned service session: %w", err)
	}
	if domain.ServiceSessionStatus(queued.Status) != domain.ServiceSessionStatusOpen {
		return nil
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		var member *serviceassignment.Member
		if queued.AssigneeIdentityID == nil {
			if member, err = serviceassignment.LockQueueMember(ctx, tx, input.OrganizationID, queued.ID, queued.TeamID, ""); err != nil {
				return err
			}
		}
		service, err := chatstate.LoadServiceConversation(ctx, tx, input.OrganizationID, queued.ConversationID)
		if err != nil {
			return err
		}
		channelSource := domain.ServiceSource(service.Source) == domain.ServiceSourceChannel
		// Telegram 先锁渠道和渠道身份，再锁会话。
		var deliveryRoute deliveryaction.Route
		if channelSource {
			if deliveryRoute, err = deliveryaction.Prepare(ctx, tx, input.OrganizationID, queued.ConversationID); err != nil {
				return err
			}
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, input.OrganizationID, queued.ConversationID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if session.ID != queued.ID || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
			return nil
		}
		noticed, err := tx.NewSelect().Model((*servermodels.Message)(nil)).
			Where("msg.organization_id = ? AND msg.conversation_id = ? AND msg.idempotency_key = ?", input.OrganizationID, session.ConversationID, input.NoticeKey).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check returned handoff notice: %w", err)
		}
		if noticed {
			return nil
		}
		// 所属队列在读取后变化时由引起变化的操作负责分配。
		if member != nil && session.AssigneeIdentityID == nil && chatstate.SameTeam(session.TeamID, queued.TeamID) {
			if err := serviceassignment.Assign(ctx, tx, locked, member); err != nil {
				return err
			}
		}
		// 其他来源的发起人已从退回事件与分配事件看到服务进度。
		if !channelSource {
			return nil
		}
		var assigneeName *string
		if session.AssigneeIdentityID != nil {
			assignee := &servermodels.OrganizationIdentity{}
			if err := tx.NewSelect().Model(assignee).Column("oi.type", "oi.display_name").
				Where("oi.organization_id = ? AND oi.id = ?", input.OrganizationID, *session.AssigneeIdentityID).
				Scan(ctx); err != nil {
				return fmt.Errorf("load returned session assignee: %w", err)
			}
			if domain.OrganizationIdentityType(assignee.Type) == domain.OrganizationIdentityTypeAgent {
				return nil
			}
			assigneeName = &assignee.DisplayName
		}
		channel, err := chatstate.LoadConversationChannel(ctx, tx, input.OrganizationID, session.ConversationID)
		if err != nil {
			return err
		}
		notice, err := customerHandoffNotice(ctx, tx, a.emailSender, channel, session.ConversationID, assigneeName, time.Now().UTC())
		if err != nil {
			return err
		}
		participantID, err := ensureCustomerAgentParticipant(ctx, tx, input.OrganizationID, session.ConversationID, input.AgentIdentityID)
		if err != nil {
			return err
		}
		// 对客通知不结束客户等待，写入后恢复通知前的等待起点与本轮提醒时间。
		awaitingReplySince, remindedAt := session.AwaitingReplySince, session.RemindedAt
		if _, err := appendCustomerAgentMessage(ctx, tx, a.enqueuer, agentRunPolicyContext{
			Conversation: conversation, ServiceSession: session, DeliveryRoute: deliveryRoute,
		}, &servermodels.Message{
			ID: uuid.NewV7().String(), OrganizationID: input.OrganizationID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, SenderParticipantID: &participantID,
			Type: string(domain.MessageTypeText), Body: notice, IdempotencyKey: &input.NoticeKey,
		}); err != nil {
			return fmt.Errorf("append returned handoff notice: %w", err)
		}
		if _, err := tx.NewUpdate().Model(session).
			Set("awaiting_reply_since = ?", awaitingReplySince).
			Set("reminded_at = ?", remindedAt).
			WherePK().Where("organization_id = ?", input.OrganizationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("restore returned session awaiting reply: %w", err)
		}
		session.AwaitingReplySince, session.RemindedAt = awaitingReplySince, remindedAt
		slog.Info("退回队列的客户会话已按承接结果通知客户",
			"organization_id", input.OrganizationID, "conversation_id", session.ConversationID,
			"service_session_id", session.ID, "assigned", assigneeName != nil)
		return nil
	})
}

// FinalizeReturnedHandoffFailure 在转人工承接任务耗尽重试后投递分配任务，周期仍在队列时由分配器承接；对客通知不再补发。
func (a *ExecuteAction) FinalizeReturnedHandoffFailure(ctx context.Context, input ReturnedHandoffInput, taskErr error) error {
	slog.Warn("转人工承接任务重试耗尽，改由分配任务承接",
		"organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID, "error", taskErr)
	return serviceassignment.EnqueueAssign(ctx, a.db, a.enqueuer, serviceassignment.AssignInput{
		OrganizationID: input.OrganizationID, ServiceSessionID: input.ServiceSessionID,
	})
}
