//go:build server

package servicehandoff

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
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
	WorkspaceID      string `json:"workspaceId"`
	ServiceSessionID string `json:"serviceSessionId"`
	AgentIdentityID  string `json:"agentIdentityId"` // 对客通知的发送者，即被退回的 AI 员工。
	NoticeKey        string `json:"noticeKey"`
}

// ReturnedHandoffAction 为退回队列的 AI 员工周期完成转人工承接：分配承接成员并按承接结果通知客户。
type ReturnedHandoffAction struct {
	db          *bun.DB
	enqueuer    servertask.TxEnqueuer
	emailSender customernotify.Sender
}

// NewReturnedHandoffAction 创建转人工承接任务处理；emailSender 为空表示部署未配置邮件发送。
func NewReturnedHandoffAction(db *bun.DB, enqueuer servertask.TxEnqueuer, emailSender customernotify.Sender) *ReturnedHandoffAction {
	return &ReturnedHandoffAction{db: db, enqueuer: enqueuer, emailSender: emailSender}
}

// enqueueReturnedHandoff 在调用方事务中投递转人工承接任务。
func enqueueReturnedHandoff(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, input ReturnedHandoffInput) error {
	if _, err := enqueuer.EnqueueIn(ctx, ReturnedHandoffActionName, input, servertask.EnqueueOptions{WorkspaceID: input.WorkspaceID, MaxAttempts: returnedHandoffMaxAttempts}); err != nil {
		return fmt.Errorf("enqueue returned service session handoff: %w", err)
	}
	return nil
}

// customerHandoffNotice 按客户语言、承接结果与企业工作时间生成转人工对发起人的话术，服务员工的渠道使用面向同事的话术：已有承接成员时介绍该成员，否则按是否处于工作时间告知排队或下次处理时间，访客可接收邮件通知时追加请其留下邮箱；客户语言不在对客语言中时使用渠道默认接待语言。
func customerHandoffNotice(ctx context.Context, db bun.IDB, emailSender customernotify.Sender, channel *servermodels.Channel, conversationID string, assigneeName *string) (string, error) {
	locale, err := translationaction.CustomerLocale(ctx, db, channel.WorkspaceID, conversationID, domain.CustomerLocale(channel.DefaultLocale))
	if err != nil {
		return "", err
	}
	employee := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Audience == domain.ServiceAudienceEmployee
	if assigneeName != nil {
		key := i18n.AgentCustomerHandoffAssigned
		if employee {
			key = i18n.EmployeeChannelHandoffAssigned
		}
		return i18n.LocalizeCustomerTemplate(locale, key, map[string]any{"Name": *assigneeName}), nil
	}
	notice, err := queuedHandoffNotice(ctx, db, channel.WorkspaceID, locale, employee)
	if err != nil {
		return "", err
	}
	requested, err := customernotify.EmailRequested(ctx, db, emailSender, channel.WorkspaceID, conversationID)
	if err != nil || !requested {
		return notice, err
	}
	return notice + "\n\n" + i18n.LocalizeCustomerTemplate(locale, i18n.AgentCustomerHandoffEmailRequest, nil), nil
}

// queuedHandoffNotice 生成留在队列的转人工话术：数据库时刻在工作时间内时告知排队，非工作时间告知下次处理时间；employee 为真时使用面向同事的话术。
func queuedHandoffNotice(ctx context.Context, db bun.IDB, workspaceID string, locale domain.CustomerLocale, employee bool) (string, error) {
	hours, err := customerserviceaction.LoadBusinessHours(ctx, db, workspaceID)
	if err != nil {
		return "", err
	}
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return "", err
	}
	queued, afterHours, unscheduled := i18n.AgentCustomerHandoffQueued, i18n.AgentCustomerHandoffAfterHours, i18n.AgentCustomerHandoffAfterHoursUnscheduled
	if employee {
		queued, afterHours, unscheduled = i18n.EmployeeChannelHandoffQueued, i18n.EmployeeChannelHandoffAfterHours, i18n.EmployeeChannelHandoffAfterHoursUnscheduled
	}
	if hours.Open(now) {
		return i18n.LocalizeCustomerTemplate(locale, queued, nil), nil
	}
	next, ok := hours.NextOpening(now)
	if !ok {
		return i18n.LocalizeCustomerTemplate(locale, unscheduled, nil), nil
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
	return i18n.LocalizeCustomerTemplate(locale, afterHours, map[string]any{
		"Month": int(local.Month()), "MonthName": local.Format("Jan"), "Day": local.Day(), "Clock": local.Format("15:04"), "Offset": offset,
	}), nil
}

// HandOffReturnedSession 为退回队列的 AI 员工周期完成转人工承接：先锁定可分配成员再锁会话，周期仍在原队列且无人负责时分配，渠道来源随后以原 AI 员工身份按承接结果通知客户。
// 周期已关闭、已换代或已发出该通知时直接结束；周期已由 AI 员工负责时不通知。
func (a *ReturnedHandoffAction) HandOffReturnedSession(ctx context.Context, input ReturnedHandoffInput) error {
	queued := &servermodels.ServiceSession{}
	err := a.db.NewSelect().Model(queued).Column("ss.id", "ss.conversation_id", "ss.status", "ss.team_id", "ss.assignee_identity_id").
		Where("ss.workspace_id = ? AND ss.id = ?", input.WorkspaceID, input.ServiceSessionID).
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
			if member, err = serviceassignment.LockQueueMember(ctx, tx, input.WorkspaceID, queued.ID, queued.TeamID, ""); err != nil {
				return err
			}
		}
		service, err := chatstate.LoadServiceConversation(ctx, tx, input.WorkspaceID, queued.ConversationID)
		if err != nil {
			return err
		}
		channelSource := domain.ServiceSource(service.Source) == domain.ServiceSourceChannel
		// 外部平台渠道先锁渠道和渠道身份，再锁会话。
		var deliveryRoute deliveryaction.Route
		if channelSource {
			if deliveryRoute, err = deliveryaction.Prepare(ctx, tx, input.WorkspaceID, queued.ConversationID); err != nil {
				return err
			}
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, input.WorkspaceID, queued.ConversationID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if session.ID != queued.ID || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
			return nil
		}
		noticed, err := tx.NewSelect().Model((*servermodels.Message)(nil)).
			Where("msg.workspace_id = ? AND msg.conversation_id = ? AND msg.idempotency_key = ?", input.WorkspaceID, session.ConversationID, input.NoticeKey).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check returned handoff notice: %w", err)
		}
		if noticed {
			return nil
		}
		// 所属队列在读取后变化时由引起变化的操作负责分配。
		if member != nil && session.AssigneeIdentityID == nil && chatstate.SameTeam(session.TeamID, queued.TeamID) {
			if err := serviceassignment.Assign(ctx, tx, a.enqueuer, locked, member); err != nil {
				return err
			}
		}
		// 其他来源的发起人已从退回事件与分配事件看到服务进度。
		if !channelSource {
			return nil
		}
		var assigneeName *string
		if session.AssigneeIdentityID != nil {
			assignee := &servermodels.WorkspaceIdentity{}
			if err := tx.NewSelect().Model(assignee).Column("oi.type", "oi.display_name").
				Where("oi.workspace_id = ? AND oi.id = ?", input.WorkspaceID, *session.AssigneeIdentityID).
				Scan(ctx); err != nil {
				return fmt.Errorf("load returned session assignee: %w", err)
			}
			if domain.WorkspaceIdentityType(assignee.Type) == domain.WorkspaceIdentityTypeAgent {
				return nil
			}
			assigneeName = &assignee.DisplayName
		}
		channel, err := serviceroute.LoadConversationChannel(ctx, tx, input.WorkspaceID, session.ConversationID)
		if err != nil {
			return err
		}
		notice, err := customerHandoffNotice(ctx, tx, a.emailSender, channel, session.ConversationID, assigneeName)
		if err != nil {
			return err
		}
		participantID, err := agentmessage.EnsureCustomerParticipant(ctx, tx, input.WorkspaceID, session.ConversationID, input.AgentIdentityID)
		if err != nil {
			return err
		}
		// 对客通知不结束客户等待，写入后恢复通知前的等待起点与本轮提醒时间。
		awaitingReplySince, remindedAt := session.AwaitingReplySince, session.RemindedAt
		if _, err := agentmessage.AppendCustomer(ctx, tx, a.enqueuer, conversation, session, deliveryRoute, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: input.WorkspaceID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, SenderParticipantID: &participantID,
			Type: string(domain.MessageTypeText), Body: notice, IdempotencyKey: &input.NoticeKey,
		}); err != nil {
			return fmt.Errorf("append returned handoff notice: %w", err)
		}
		if err := servicestate.Begin(session).ResumeAwaiting(awaitingReplySince, remindedAt).Save(ctx, tx, a.enqueuer); err != nil {
			return err
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "退回队列的客户会话已按承接结果通知客户",
			"conversation_id", session.ConversationID,
			"service_session_id", session.ID, "assigned", assigneeName != nil)
		return nil
	})
}

// FinalizeReturnedHandoffFailure 在转人工承接任务耗尽重试后投递分配任务，周期仍在队列时由分配器承接，不补发对客通知。
func (a *ReturnedHandoffAction) FinalizeReturnedHandoffFailure(ctx context.Context, input ReturnedHandoffInput, taskErr error) error {
	slog.WarnContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "转人工承接任务重试耗尽，改由分配任务承接",
		"service_session_id", input.ServiceSessionID, "error", taskErr)
	return serviceassignment.EnqueueAssign(ctx, a.db, a.enqueuer, serviceassignment.AssignInput{
		WorkspaceID: input.WorkspaceID, ServiceSessionID: input.ServiceSessionID,
	})
}
