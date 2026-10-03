//go:build server

// Package servicetimeout 按企业超时设置提醒负责人和队列客服处理等待中的客户，回收负责人超时未回复的客服处理周期，并在客户超时未回复 AI 员工时跟进与关单。
package servicetimeout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// ScanActionName 扫描各企业到达超时时长的开放客服处理周期。
	ScanActionName = "service_session.timeout_scan"
	// ProcessActionName 处理单个客服处理周期的超时提醒与回收。
	ProcessActionName = "service_session.timeout"
	// ScheduleKey 是超时扫描的定时计划键。
	ScheduleKey = "customer-service-timeout-scan"
)

// scanLimit 是单次扫描投递的周期上限，其余周期由下一次扫描继续处理。
const scanLimit = 200

// ProcessInput 定义单个客服处理周期的超时处理任务。
type ProcessInput struct {
	OrganizationID   string `json:"organizationId"`
	ServiceSessionID string `json:"serviceSessionId"`
}

// Enqueuer 投递单条处理任务和事务内的补分配任务。
type Enqueuer interface {
	servertask.Enqueuer
	servertask.TxEnqueuer
}

// FollowUpScheduler 在调用方已锁定会话的事务内为 AI 员工负责的周期追加超时跟进输入。
type FollowUpScheduler interface {
	ScheduleCustomerFollowUp(context.Context, bun.IDB, *servermodels.ServiceSession) (bool, error)
}

// Worker 执行客服处理周期的超时扫描与单条处理任务。
type Worker struct {
	db        *bun.DB
	enqueuer  Enqueuer
	scheduler FollowUpScheduler
}

// NewWorker 创建客服处理周期超时任务执行器。
func NewWorker(db *bun.DB, enqueuer Enqueuer, scheduler FollowUpScheduler) *Worker {
	return &Worker{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// agentIdleCondition 筛选 AI 员工负责、客户没有待回复消息、最后一条对客消息来自该 AI 员工且没有在途运行的周期。
const agentIdleCondition = `oi.type = ? AND ss.awaiting_reply_since IS NULL
	AND EXISTS (
		SELECT 1 FROM messages AS lm
		JOIN conversation_participants AS lcp ON lcp.organization_id = lm.organization_id AND lcp.id = lm.sender_participant_id
		JOIN chat_subjects AS lcs ON lcs.organization_id = lcp.organization_id AND lcs.id = lcp.subject_id
		WHERE lm.organization_id = ss.organization_id AND lm.id = ss.last_message_id AND lcs.kind = ? AND lcs.source_id = ss.assignee_identity_id
	)
	AND NOT EXISTS (
		SELECT 1 FROM agent_runs AS agr
		WHERE agr.organization_id = ss.organization_id AND agr.scope_kind = ? AND agr.scope_id = ss.id AND agr.status IN (?)
	)`

// agentIdleArgs 返回 agentIdleCondition 的参数。
func agentIdleArgs() []any {
	return []any{domain.OrganizationIdentityTypeAgent, domain.ChatSubjectKindOrganizationIdentity,
		domain.AgentExecutionScopeServiceSession, bun.In(domain.AgentRunActiveStatuses)}
}

// Scan 跨正常状态的企业读取到达提醒、回收、AI 跟进或 AI 关单时长的开放周期，按周期投递单条处理任务；队列提醒只选取有可提醒客服的周期，同一周期在途时不重复投递。
func (w *Worker) Scan(ctx context.Context, _ struct{}) error {
	start := "GREATEST(ss.awaiting_reply_since, ss.assignee_assigned_at)"
	var rows []ProcessInput
	err := w.db.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("ss.organization_id, ss.id AS service_session_id").
		Join("JOIN customer_service_settings AS css ON css.organization_id = ss.organization_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.organization_id = ss.organization_id AND oi.id = ss.assignee_identity_id").
		Where("ss.status = ?", domain.ServiceSessionStatusOpen).
		WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.
				WhereGroup(" OR ", func(query *bun.SelectQuery) *bun.SelectQuery {
					return query.Where("ss.awaiting_reply_since IS NOT NULL").
						WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
							return query.
								Where("oi.type = ? AND ss.reminded_at IS NULL AND "+start+" <= now() - make_interval(mins => css.response_reminder_minutes)",
									domain.OrganizationIdentityTypeUser).
								WhereOr("oi.type = ? AND "+start+" <= now() - make_interval(mins => css.response_reclaim_minutes)",
									domain.OrganizationIdentityTypeUser).
								WhereOr("ss.assignee_identity_id IS NULL AND ss.reminded_at IS NULL AND ss.awaiting_reply_since <= now() - make_interval(mins => css.queue_reminder_minutes) AND EXISTS (?)",
									queueReminderRecipients(w.db, bun.Ident("ss.organization_id"), bun.Ident("ss.team_id")).ColumnExpr("1"))
						})
				}).
				WhereGroup(" OR ", func(query *bun.SelectQuery) *bun.SelectQuery {
					return query.Where(agentIdleCondition, agentIdleArgs()...).
						WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
							return query.
								Where("(ss.resolution_requested_at IS NULL OR ss.resolution_requested_at < ss.assignee_assigned_at) AND GREATEST(ss.last_message_at, ss.assignee_assigned_at) <= now() - make_interval(mins => css.ai_follow_up_minutes)").
								WhereOr("ss.resolution_requested_at >= COALESCE(ss.assignee_assigned_at, ss.resolution_requested_at) AND GREATEST(ss.last_message_at, ss.resolution_requested_at) <= now() - make_interval(mins => css.ai_close_minutes)")
						})
				})
		}).
		Where(identityaction.ActiveWorkspaceCondition("ss.organization_id")).
		OrderExpr("COALESCE(ss.awaiting_reply_since, ss.last_message_at) ASC, ss.id ASC").
		Limit(scanLimit).
		Scan(ctx, &rows)
	if err != nil {
		return fmt.Errorf("scan service session timeouts: %w", err)
	}
	for _, row := range rows {
		if _, err := w.enqueuer.Enqueue(ctx, ProcessActionName, row, servertask.EnqueueOptions{OrganizationID: row.OrganizationID, MaxAttempts: 3, IdempotencyKey: "service-timeout:" + row.ServiceSessionID}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("投递客服处理周期超时任务失败", "organization_id", row.OrganizationID, "service_session_id", row.ServiceSessionID, "error", err)
		}
	}
	return nil
}

// timeoutAction 表示单个周期当前应执行的超时动作。
type timeoutAction int

const (
	// actionNone 表示周期未到达任何超时时长或已提醒过。
	actionNone timeoutAction = iota
	// actionRemindAssignee 表示提醒负责人回复。
	actionRemindAssignee
	// actionReclaim 表示退回队列并重新分配。
	actionReclaim
	// actionRemindQueue 表示提醒队列对应的客服。
	actionRemindQueue
	// actionFollowUp 表示由 AI 负责人跟进一次并请客户确认问题是否解决。
	actionFollowUp
	// actionCloseUnresponsive 表示按客户失联关闭周期。
	actionCloseUnresponsive
)

// dueAction 按周期当前状态和企业超时时长判断应执行的动作；assigneeType 为负责人身份类型，周期在队列中时为空；agentIdle 表示 AI 负责人是最后一条对客消息的发送者且没有在途运行。
func dueAction(session *servermodels.ServiceSession, assigneeType domain.OrganizationIdentityType, agentIdle bool, timeouts domain.ServiceTimeouts, now time.Time) timeoutAction {
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
		return actionNone
	}
	// AI 负责人发言后客户未回复：先跟进一次并请客户确认，确认请求之后仍未回复则按客户失联关闭。
	// 计时从最后一条对客消息与当前负责人接手时间中较晚者起算，确认请求只计当前负责人接手之后发出的。
	if assigneeType == domain.OrganizationIdentityTypeAgent {
		if session.AwaitingReplySince != nil || !agentIdle {
			return actionNone
		}
		start := session.LastMessageAt
		if session.AssigneeAssignedAt != nil && session.AssigneeAssignedAt.After(start) {
			start = *session.AssigneeAssignedAt
		}
		if session.ResolutionRequestedAt == nil || (session.AssigneeAssignedAt != nil && session.ResolutionRequestedAt.Before(*session.AssigneeAssignedAt)) {
			if !now.Before(start.Add(time.Duration(timeouts.AIFollowUpMinutes) * time.Minute)) {
				return actionFollowUp
			}
			return actionNone
		}
		if session.ResolutionRequestedAt.After(start) {
			start = *session.ResolutionRequestedAt
		}
		if !now.Before(start.Add(time.Duration(timeouts.AICloseMinutes) * time.Minute)) {
			return actionCloseUnresponsive
		}
		return actionNone
	}
	if session.AwaitingReplySince == nil {
		return actionNone
	}
	if session.AssigneeIdentityID == nil {
		if session.RemindedAt == nil && !now.Before(session.AwaitingReplySince.Add(time.Duration(timeouts.QueueReminderMinutes)*time.Minute)) {
			return actionRemindQueue
		}
		return actionNone
	}
	if assigneeType != domain.OrganizationIdentityTypeUser {
		return actionNone
	}
	// 未响应计时从客户开始等待与负责人接手中较晚的时间起算。
	start := *session.AwaitingReplySince
	if session.AssigneeAssignedAt != nil && session.AssigneeAssignedAt.After(start) {
		start = *session.AssigneeAssignedAt
	}
	if !now.Before(start.Add(time.Duration(timeouts.ResponseReclaimMinutes) * time.Minute)) {
		return actionReclaim
	}
	if session.RemindedAt == nil && !now.Before(start.Add(time.Duration(timeouts.ResponseReminderMinutes)*time.Minute)) {
		return actionRemindAssignee
	}
	return actionNone
}

// Process 读取周期与企业超时时长后执行到期动作；每个动作在事务内锁定会话并复核条件，条件已不满足时直接结束。
func (w *Worker) Process(ctx context.Context, input ProcessInput) error {
	timeouts, err := customerservice.LoadServiceTimeouts(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	loaded, err := loadSession(ctx, w.db, input.OrganizationID, input.ServiceSessionID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch loaded.dueAction(timeouts, time.Now()) {
	case actionReclaim:
		return w.reclaim(ctx, loaded.Session, timeouts)
	case actionRemindAssignee, actionRemindQueue:
		return w.remind(ctx, input, timeouts)
	case actionFollowUp, actionCloseUnresponsive:
		return w.handleAgentIdle(ctx, input, timeouts)
	default:
		return nil
	}
}

// loadedSession 是超时处理读取的周期、加锁结果与负责人状态；Locked 只在加锁读取时给出。
type loadedSession struct {
	Locked       chatstate.LockedServiceSession
	Session      *servermodels.ServiceSession
	AssigneeType domain.OrganizationIdentityType
	AgentIdle    bool
}

// dueAction 按读取到的周期状态判断应执行的动作。
func (l loadedSession) dueAction(timeouts domain.ServiceTimeouts, now time.Time) timeoutAction {
	return dueAction(l.Session, l.AssigneeType, l.AgentIdle, timeouts, now)
}

// loadSession 读取客服处理周期、负责人身份类型与 AI 负责人是否空闲；lock 为 true 时先锁定并返回所属会话与当前周期，会话已开始新的周期时返回 sql.ErrNoRows。
func loadSession(ctx context.Context, db bun.IDB, organizationID, serviceSessionID string, lock bool) (loadedSession, error) {
	var loaded loadedSession
	if lock {
		locked, err := chatstate.LockServiceSessionByID(ctx, db, organizationID, serviceSessionID)
		if errors.Is(err, chatstate.ErrServiceSessionNotFound) {
			return loadedSession{}, sql.ErrNoRows
		}
		if err != nil {
			return loadedSession{}, err
		}
		if current := locked.Service.CurrentServiceSessionID; current == nil || *current != serviceSessionID {
			return loadedSession{}, sql.ErrNoRows
		}
		loaded = loadedSession{Locked: locked, Session: locked.Session}
	} else {
		session := &servermodels.ServiceSession{}
		if err := db.NewSelect().Model(session).
			Where("ss.organization_id = ? AND ss.id = ?", organizationID, serviceSessionID).
			Scan(ctx); err != nil {
			return loadedSession{}, err
		}
		loaded = loadedSession{Session: session}
	}
	if loaded.Session.AssigneeIdentityID == nil {
		return loaded, nil
	}
	if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).Column("oi.type").
		Where("oi.organization_id = ? AND oi.id = ?", organizationID, *loaded.Session.AssigneeIdentityID).
		Scan(ctx, &loaded.AssigneeType); err != nil {
		return loadedSession{}, fmt.Errorf("load service session assignee type: %w", err)
	}
	if loaded.AssigneeType != domain.OrganizationIdentityTypeAgent {
		return loaded, nil
	}
	if err := db.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("EXISTS (SELECT 1 FROM organization_identities AS oi WHERE oi.organization_id = ss.organization_id AND oi.id = ss.assignee_identity_id AND "+agentIdleCondition+")", agentIdleArgs()...).
		Where("ss.organization_id = ? AND ss.id = ?", organizationID, serviceSessionID).
		Scan(ctx, &loaded.AgentIdle); err != nil {
		return loadedSession{}, fmt.Errorf("load service session agent idle state: %w", err)
	}
	return loaded, nil
}

// queueReminderRecipients 构造队列提醒收件人查询：所属团队中或全企业工作中的有效接待成员，teamID 为空表示公共队列；u 为成员账号别名。
func queueReminderRecipients(db bun.IDB, organizationID, teamID any) *bun.SelectQuery {
	query := db.NewSelect().TableExpr("organization_identities AS oi").
		Join("JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
		Where("oi.organization_id = ? AND oi.type = ? AND oi.work_status = ?", organizationID, domain.OrganizationIdentityTypeUser, domain.WorkStatusWorking).
		Where("(? IS NULL OR EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.organization_id = oi.organization_id AND tm.identity_id = oi.id AND tm.team_id = ?))", teamID, teamID)
	return identityaction.ApplyServiceHandlingConditions(query)
}

// remind 在事务中锁定周期并复核仍需提醒后写入提醒时间，提交后提醒负责人或队列对应的工作中客服；没有收件人时本轮提醒保持未发出。
func (w *Worker) remind(ctx context.Context, input ProcessInput, timeouts domain.ServiceTimeouts) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.OrganizationID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		action := loaded.dueAction(timeouts, time.Now())
		if action != actionRemindAssignee && action != actionRemindQueue {
			return nil
		}
		reason := domain.ServiceAttentionResponseOverdue
		// 负责人提醒只发给负责人；队列提醒发给团队中或全企业工作中的客服。
		var recipients *bun.SelectQuery
		if action == actionRemindAssignee {
			recipients = tx.NewSelect().TableExpr("organization_identities AS oi").
				Join("JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
				Where("oi.organization_id = ? AND oi.type = ? AND oi.id = ?", session.OrganizationID, domain.OrganizationIdentityTypeUser, *session.AssigneeIdentityID)
		} else {
			reason = domain.ServiceAttentionQueueWaiting
			recipients = queueReminderRecipients(tx, session.OrganizationID, session.TeamID)
		}
		var userIDs []string
		if err := recipients.ColumnExpr("u.id").Scan(ctx, &userIDs); err != nil {
			return fmt.Errorf("load service session reminder recipients: %w", err)
		}
		// 没有收件人时不记录提醒，由之后的扫描在有人工作时补发。
		if len(userIDs) == 0 {
			return nil
		}
		if _, err := tx.NewUpdate().Model(session).
			Set("reminded_at = now()").
			Set("updated_at = now()").
			WherePK().Where("organization_id = ?", session.OrganizationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("record service session reminder: %w", err)
		}
		for _, userID := range userIDs {
			realtime.Notify(ctx, realtime.UserServiceAttention(session.OrganizationID, userID, session.ConversationID, session.ID, reason))
		}
		slog.Info("客服处理周期等待超时，已提醒",
			"organization_id", session.OrganizationID, "conversation_id", session.ConversationID,
			"service_session_id", session.ID, "reason", reason, "recipient_count", len(userIDs))
		return nil
	})
}

// reclaim 把负责人超时未回复的周期退回原队列：先锁定排除原负责人后的候选成员、再锁会话并复核，写入退回事件后分配给候选成员，没有候选时留在队列并投递排除原负责人的分配任务；提交后告知原负责人并为其补分配。
func (w *Worker) reclaim(ctx context.Context, snapshot *servermodels.ServiceSession, timeouts domain.ServiceTimeouts) error {
	previousAssigneeID := *snapshot.AssigneeIdentityID
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		member, err := serviceassignment.LockQueueMember(ctx, tx, snapshot.OrganizationID, snapshot.ID, snapshot.TeamID, previousAssigneeID)
		if err != nil {
			return err
		}
		loaded, err := loadSession(ctx, tx, snapshot.OrganizationID, snapshot.ID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		conversation, session := loaded.Locked.Conversation, loaded.Session
		// 负责人或队列在读取后已变化时由引起变化的操作负责后续处理。
		if loaded.dueAction(timeouts, time.Now()) != actionReclaim ||
			*session.AssigneeIdentityID != previousAssigneeID || !chatstate.SameTeam(session.TeamID, snapshot.TeamID) {
			return nil
		}
		previous := &servermodels.OrganizationIdentity{}
		if err := tx.NewSelect().Model(previous).Column("oi.id", "oi.display_name").
			Where("oi.organization_id = ? AND oi.id = ?", session.OrganizationID, previousAssigneeID).
			Scan(ctx); err != nil {
			return fmt.Errorf("load reclaimed assignee: %w", err)
		}
		target, err := chatstate.ServiceSessionQueueTarget(ctx, tx, session)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(domain.ServiceSessionReturnedEvent{
			ServiceSessionID: session.ID, FromIdentityID: previous.ID, FromDisplayName: previous.DisplayName,
			Target: target, Reason: domain.ServiceSessionReturnResponseTimeout,
		})
		if err != nil {
			return fmt.Errorf("encode service session returned event: %w", err)
		}
		eventType := string(domain.ConversationSystemEventServiceSessionReturned)
		if _, _, err := chatstate.AppendMessage(ctx, tx, conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), OrganizationID: session.OrganizationID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
			SystemEventType: &eventType, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("append service session returned event: %w", err)
		}
		if _, err := chatstate.AppendRequesterStatus(ctx, tx, conversation, session, loaded.Locked.Source(), domain.ServiceRequestStatusHandedOff, &target, nil); err != nil {
			return err
		}
		if err := chatstate.ReturnServiceSessionToQueue(ctx, tx, session, session.TeamID, time.Now().UTC()); err != nil {
			return err
		}
		// 没有候选时投递排除原负责人的分配任务，覆盖回收提交前已完成补分配的成员。
		if member != nil {
			if err := serviceassignment.Assign(ctx, tx, loaded.Locked, member); err != nil {
				return err
			}
		} else if err := serviceassignment.EnqueueAssign(ctx, tx, w.enqueuer, serviceassignment.AssignInput{
			OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, ExcludeIdentityID: previousAssigneeID,
		}); err != nil {
			return err
		}
		if err := serviceassignment.EnqueueBackfill(ctx, tx, w.enqueuer, serviceassignment.BackfillInput{
			OrganizationID: session.OrganizationID, IdentityID: previousAssigneeID, ExcludeServiceSessionID: session.ID,
		}); err != nil {
			return err
		}
		// 读取原负责人账号，提醒其周期已退回队列。
		var previousUserID string
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("u.id").
			Where("u.organization_id = ? AND u.identity_id = ?", session.OrganizationID, previousAssigneeID).
			Scan(ctx, &previousUserID); err != nil {
			return fmt.Errorf("load reclaimed assignee user: %w", err)
		}
		realtime.Notify(ctx, realtime.UserServiceAttention(session.OrganizationID, previousUserID, session.ConversationID, session.ID, domain.ServiceAttentionReturned))
		slog.Info("负责人超时未回复，客服处理周期已退回队列",
			"organization_id", session.OrganizationID, "conversation_id", session.ConversationID,
			"service_session_id", session.ID, "previous_assignee_identity_id", previousAssigneeID,
			"target_kind", target.Kind, "reassigned", member != nil)
		return nil
	})
}

// handleAgentIdle 在事务中锁定周期并复核客户仍未回复 AI 负责人：到达跟进时长时追加一次超时跟进输入，确认请求后到达关单时长时按客户失联关闭周期。
func (w *Worker) handleAgentIdle(ctx context.Context, input ProcessInput, timeouts domain.ServiceTimeouts) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.OrganizationID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		switch loaded.dueAction(timeouts, time.Now()) {
		case actionFollowUp:
			scheduled, err := w.scheduler.ScheduleCustomerFollowUp(ctx, tx, session)
			if err != nil {
				return err
			}
			slog.Info("客户超时未回复 AI 员工，处理超时跟进",
				"organization_id", session.OrganizationID, "conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID, "scheduled", scheduled)
		case actionCloseUnresponsive:
			if err := servicesessionaction.CloseAgentServiceSession(ctx, tx, w.enqueuer, loaded.Locked.Conversation, session, loaded.Locked.Source(), domain.ServiceSessionCloseCustomerUnresponsive); err != nil {
				return err
			}
			slog.Info("客户确认请求后超时未回复，客服处理周期已关闭",
				"organization_id", session.OrganizationID, "conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID)
		}
		return nil
	})
}
