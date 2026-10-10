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
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
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

// scanPageSize 是超时扫描每页读取的候选周期数。
const scanPageSize = 500

// ProcessInput 定义单个客服处理周期的超时处理任务。
type ProcessInput struct {
	WorkspaceID      string `json:"workspaceId"`
	ServiceSessionID string `json:"serviceSessionId"`
}

// FollowUpScheduler 在调用方已锁定会话的事务内为 AI 员工负责的周期追加超时跟进输入。
type FollowUpScheduler interface {
	ScheduleCustomerFollowUp(context.Context, bun.IDB, *servermodels.ServiceSession) (bool, error)
}

// Worker 执行客服处理周期的超时扫描与单条处理任务。
type Worker struct {
	db        *bun.DB
	enqueuer  servertask.DualEnqueuer
	scheduler FollowUpScheduler
}

// NewWorker 创建客服处理周期超时任务执行器。
func NewWorker(db *bun.DB, enqueuer servertask.DualEnqueuer, scheduler FollowUpScheduler) *Worker {
	return &Worker{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// agentIdleCondition 筛选 AI 员工负责、客户没有待回复消息、最后一条对客消息来自该 AI 员工且没有在途运行的周期。
const agentIdleCondition = `oi.type = ? AND ss.awaiting_reply_since IS NULL
	AND EXISTS (
		SELECT 1 FROM messages AS lm
		JOIN conversation_participants AS lcp ON lcp.workspace_id = lm.workspace_id AND lcp.id = lm.sender_participant_id
		JOIN chat_subjects AS lcs ON lcs.workspace_id = lcp.workspace_id AND lcs.id = lcp.subject_id
		WHERE lm.workspace_id = ss.workspace_id AND lm.id = ss.last_message_id AND lcs.kind = ? AND lcs.source_id = ss.assignee_identity_id
	)
	AND NOT EXISTS (
		SELECT 1 FROM agent_runs AS agr
		WHERE agr.workspace_id = ss.workspace_id AND agr.scope_kind = ? AND agr.scope_id = ss.id AND agr.status IN (?)
	)`

// agentIdleArgs 返回 agentIdleCondition 的参数。
func agentIdleArgs() []any {
	return []any{domain.WorkspaceIdentityTypeAgent, domain.ChatSubjectKindWorkspaceIdentity,
		domain.AgentExecutionScopeServiceSession, bun.List(domain.AgentRunActiveStatuses)}
}

// scanCandidate 是超时扫描读取的候选周期、负责人状态与所属企业的超时时长。
type scanCandidate struct {
	WorkspaceID             string                       `bun:"workspace_id"`
	ID                      string                       `bun:"id"`
	Status                  string                       `bun:"status"`
	AssigneeIdentityID      *string                      `bun:"assignee_identity_id"`
	LastMessageAt           time.Time                    `bun:"last_message_at"`
	AssigneeAssignedAt      *time.Time                   `bun:"assignee_assigned_at"`
	AwaitingReplySince      *time.Time                   `bun:"awaiting_reply_since"`
	RemindedAt              *time.Time                   `bun:"reminded_at"`
	ResolutionRequestedAt   *time.Time                   `bun:"resolution_requested_at"`
	AssigneeType            domain.WorkspaceIdentityType `bun:"assignee_type"`
	AgentIdle               bool                         `bun:"agent_idle"`
	HasQueueRecipients      bool                         `bun:"has_queue_recipients"`
	ResponseReminderMinutes int                          `bun:"response_reminder_minutes"`
	ResponseReclaimMinutes  int                          `bun:"response_reclaim_minutes"`
	QueueReminderMinutes    int                          `bun:"queue_reminder_minutes"`
	AIFollowUpMinutes       int                          `bun:"ai_follow_up_minutes"`
	AICloseMinutes          int                          `bun:"ai_close_minutes"`
	OrderAt                 time.Time                    `bun:"order_at"`
}

// Scan 跨正常状态的企业按开始等待时间先后遍历开放周期，以 dueAction 选出到达提醒、回收、AI 跟进或 AI 关单时长的周期并投递单条处理任务，每轮最多投递 scanLimit 个；队列提醒只选取有可提醒客服的周期，同一周期在途时不重复投递。
func (w *Worker) Scan(ctx context.Context, _ struct{}) error {
	now, err := serverstorage.Now(ctx, w.db)
	if err != nil {
		return err
	}
	var due []ProcessInput
	var cursor *scanCandidate
	for len(due) < scanLimit {
		candidates, err := w.scanCandidates(ctx, now, cursor)
		if err != nil {
			return err
		}
		for index := range candidates {
			candidate := &candidates[index]
			session := &servermodels.ServiceSession{
				ID: candidate.ID, WorkspaceID: candidate.WorkspaceID, Status: candidate.Status,
				AssigneeIdentityID: candidate.AssigneeIdentityID, LastMessageAt: candidate.LastMessageAt,
				AssigneeAssignedAt: candidate.AssigneeAssignedAt, AwaitingReplySince: candidate.AwaitingReplySince,
				RemindedAt: candidate.RemindedAt, ResolutionRequestedAt: candidate.ResolutionRequestedAt,
			}
			timeouts := domain.ServiceTimeouts{
				ResponseReminderMinutes: candidate.ResponseReminderMinutes, ResponseReclaimMinutes: candidate.ResponseReclaimMinutes,
				QueueReminderMinutes: candidate.QueueReminderMinutes, AIFollowUpMinutes: candidate.AIFollowUpMinutes, AICloseMinutes: candidate.AICloseMinutes,
			}
			action := dueAction(session, candidate.AssigneeType, candidate.AgentIdle, timeouts, now)
			if action == actionNone || (action == actionRemindQueue && !candidate.HasQueueRecipients) {
				continue
			}
			due = append(due, ProcessInput{WorkspaceID: candidate.WorkspaceID, ServiceSessionID: candidate.ID})
			if len(due) == scanLimit {
				break
			}
		}
		if len(candidates) < scanPageSize {
			break
		}
		cursor = &candidates[len(candidates)-1]
	}
	enqueued := 0
	for _, row := range due {
		if err := w.enqueuer.Enqueue(ctx, ProcessActionName, row, servertask.EnqueueOptions{WorkspaceID: row.WorkspaceID, MaxAttempts: 3, IdempotencyKey: "service-timeout:" + row.ServiceSessionID}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(logscope.WithWorkspace(ctx, row.WorkspaceID), "投递客服处理周期超时任务失败", "service_session_id", row.ServiceSessionID, "error", err)
			continue
		}
		enqueued++
	}
	if len(due) > 0 {
		slog.InfoContext(ctx, "已扫描客服处理周期超时", "due", len(due), "enqueued", enqueued)
	}
	return nil
}

// scanCandidates 按开始等待时间与周期编号读取 cursor 之后的一页候选周期；候选条件是 dueAction 返回动作的必要条件：
// 队列周期须未提醒且客户等待起点早于 now 减去队列提醒时长；真人周期须客户等待起点与负责人接手中较晚者早于 now 减去回收时长，未提醒时取提醒与回收时长中的最小值；
// 客户未待回复的 AI 周期按 dueAction 的跟进或关单起点与时长比较，AI 负责人是否空闲由 dueAction 判断。
func (w *Worker) scanCandidates(ctx context.Context, now time.Time, cursor *scanCandidate) ([]scanCandidate, error) {
	query := w.db.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("ss.workspace_id, ss.id, ss.status, ss.assignee_identity_id, ss.last_message_at, ss.assignee_assigned_at").
		ColumnExpr("ss.awaiting_reply_since, ss.reminded_at, ss.resolution_requested_at").
		ColumnExpr("COALESCE(oi.type, '') AS assignee_type").
		ColumnExpr("CASE WHEN oi.type = ? THEN "+agentIdleCondition+" ELSE false END AS agent_idle",
			append([]any{domain.WorkspaceIdentityTypeAgent}, agentIdleArgs()...)...).
		ColumnExpr("CASE WHEN ss.assignee_identity_id IS NULL THEN EXISTS (?) ELSE false END AS has_queue_recipients",
			queueReminderRecipients(w.db, bun.Ident("ss.workspace_id"), bun.Ident("ss.team_id")).ColumnExpr("1")).
		ColumnExpr("css.response_reminder_minutes, css.response_reclaim_minutes, css.queue_reminder_minutes, css.ai_follow_up_minutes, css.ai_close_minutes").
		ColumnExpr("COALESCE(ss.awaiting_reply_since, ss.last_message_at) AS order_at").
		Join("JOIN customer_service_settings AS css ON css.workspace_id = ss.workspace_id").
		Join("LEFT JOIN workspace_identities AS oi ON oi.workspace_id = ss.workspace_id AND oi.id = ss.assignee_identity_id").
		Where("ss.status = ?", domain.ServiceSessionStatusOpen).
		WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.
				Where("ss.assignee_identity_id IS NULL AND ss.reminded_at IS NULL AND ss.awaiting_reply_since <= ?::timestamptz - make_interval(mins => css.queue_reminder_minutes)", now).
				WhereOr(`oi.type = ? AND ss.awaiting_reply_since IS NOT NULL AND GREATEST(ss.awaiting_reply_since, ss.assignee_assigned_at) <= ?::timestamptz - make_interval(mins =>
					CASE WHEN ss.reminded_at IS NULL THEN LEAST(css.response_reminder_minutes, css.response_reclaim_minutes) ELSE css.response_reclaim_minutes END)`,
					domain.WorkspaceIdentityTypeUser, now).
				WhereOr(`oi.type = ? AND ss.awaiting_reply_since IS NULL AND CASE WHEN ss.resolution_requested_at IS NULL OR ss.resolution_requested_at < ss.assignee_assigned_at
					THEN GREATEST(ss.last_message_at, ss.assignee_assigned_at) <= ?::timestamptz - make_interval(mins => css.ai_follow_up_minutes)
					ELSE GREATEST(ss.last_message_at, ss.assignee_assigned_at, ss.resolution_requested_at) <= ?::timestamptz - make_interval(mins => css.ai_close_minutes) END`,
					domain.WorkspaceIdentityTypeAgent, now, now)
		}).
		Where(identityaction.ActiveWorkspaceCondition("ss.workspace_id"))
	if cursor != nil {
		query = query.Where("(COALESCE(ss.awaiting_reply_since, ss.last_message_at), ss.id) > (?::timestamptz, ?::uuid)", cursor.OrderAt, cursor.ID)
	}
	var candidates []scanCandidate
	if err := query.OrderExpr("order_at ASC, ss.id ASC").Limit(scanPageSize).Scan(ctx, &candidates); err != nil {
		return nil, fmt.Errorf("scan service session timeout candidates: %w", err)
	}
	return candidates, nil
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
func dueAction(session *servermodels.ServiceSession, assigneeType domain.WorkspaceIdentityType, agentIdle bool, timeouts domain.ServiceTimeouts, now time.Time) timeoutAction {
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
		return actionNone
	}
	// AI 负责人发言后客户未回复：先跟进一次并请客户确认，确认请求之后仍未回复则按客户失联关闭。
	// 计时从最后一条对客消息与当前负责人接手时间中较晚者起算，确认请求只计当前负责人接手之后发出的。
	if assigneeType == domain.WorkspaceIdentityTypeAgent {
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
	if assigneeType != domain.WorkspaceIdentityTypeUser {
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
	timeouts, err := customerservice.LoadServiceTimeouts(ctx, w.db, input.WorkspaceID)
	if err != nil {
		return err
	}
	loaded, err := loadSession(ctx, w.db, input.WorkspaceID, input.ServiceSessionID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch loaded.dueAction(timeouts) {
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

// loadedSession 是超时处理读取的周期、加锁结果、负责人状态与读取时的数据库时刻；Locked 只在加锁读取时给出。
type loadedSession struct {
	Locked       chatstate.LockedServiceSession
	Session      *servermodels.ServiceSession
	AssigneeType domain.WorkspaceIdentityType
	AgentIdle    bool
	Now          time.Time
}

// dueAction 按读取到的周期状态与读取时的数据库时刻判断应执行的动作。
func (l loadedSession) dueAction(timeouts domain.ServiceTimeouts) timeoutAction {
	return dueAction(l.Session, l.AssigneeType, l.AgentIdle, timeouts, l.Now)
}

// loadSession 读取客服处理周期、负责人身份类型与 AI 负责人是否空闲；lock 为 true 时先锁定并返回所属会话与当前周期，会话已开始新的周期时返回 sql.ErrNoRows。
func loadSession(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID string, lock bool) (loadedSession, error) {
	var loaded loadedSession
	if lock {
		locked, err := chatstate.LockServiceSessionByID(ctx, db, workspaceID, serviceSessionID)
		if errors.Is(err, servicestate.ErrServiceSessionNotFound) {
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
			Where("ss.workspace_id = ? AND ss.id = ?", workspaceID, serviceSessionID).
			Scan(ctx); err != nil {
			return loadedSession{}, err
		}
		loaded = loadedSession{Session: session}
	}
	// 到期判断取读取周期之后的数据库时刻，加锁读取时为等待行锁之后的时刻。
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return loadedSession{}, err
	}
	loaded.Now = now
	if loaded.Session.AssigneeIdentityID == nil {
		return loaded, nil
	}
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).Column("oi.type").
		Where("oi.workspace_id = ? AND oi.id = ?", workspaceID, *loaded.Session.AssigneeIdentityID).
		Scan(ctx, &loaded.AssigneeType); err != nil {
		return loadedSession{}, fmt.Errorf("load service session assignee type: %w", err)
	}
	if loaded.AssigneeType != domain.WorkspaceIdentityTypeAgent {
		return loaded, nil
	}
	if err := db.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("EXISTS (SELECT 1 FROM workspace_identities AS oi WHERE oi.workspace_id = ss.workspace_id AND oi.id = ss.assignee_identity_id AND "+agentIdleCondition+")", agentIdleArgs()...).
		Where("ss.workspace_id = ? AND ss.id = ?", workspaceID, serviceSessionID).
		Scan(ctx, &loaded.AgentIdle); err != nil {
		return loadedSession{}, fmt.Errorf("load service session agent idle state: %w", err)
	}
	return loaded, nil
}

// queueReminderRecipients 构造队列提醒收件人查询：所属团队中或全企业工作中的有效接待成员，teamID 为空表示公共队列；u 为成员账号别名。
func queueReminderRecipients(db bun.IDB, workspaceID, teamID any) *bun.SelectQuery {
	query := db.NewSelect().TableExpr("workspace_identities AS oi").
		Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
		Where("oi.workspace_id = ? AND oi.type = ? AND oi.work_status = ?", workspaceID, domain.WorkspaceIdentityTypeUser, domain.WorkStatusWorking).
		Where("(? IS NULL OR EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.workspace_id = oi.workspace_id AND tm.identity_id = oi.id AND tm.team_id = ?))", teamID, teamID)
	return serviceroute.ApplyServiceHandlingUserConditions(query)
}

// remind 在事务中锁定周期并复核仍需提醒后写入提醒时间，并为负责人或队列对应的工作中客服登记提醒通知任务；没有收件人时本轮提醒保持未发出。
func (w *Worker) remind(ctx context.Context, input ProcessInput, timeouts domain.ServiceTimeouts) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.WorkspaceID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		action := loaded.dueAction(timeouts)
		if action != actionRemindAssignee && action != actionRemindQueue {
			return nil
		}
		reason := domain.ServiceAttentionResponseOverdue
		// 负责人提醒只发给负责人；队列提醒发给团队中或全企业工作中的客服。
		var recipients *bun.SelectQuery
		if action == actionRemindAssignee {
			recipients = tx.NewSelect().TableExpr("workspace_identities AS oi").
				Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
				Where("oi.workspace_id = ? AND oi.type = ? AND oi.id = ?", session.WorkspaceID, domain.WorkspaceIdentityTypeUser, *session.AssigneeIdentityID)
		} else {
			reason = domain.ServiceAttentionQueueWaiting
			recipients = queueReminderRecipients(tx, session.WorkspaceID, session.TeamID)
		}
		var userIDs []string
		if err := recipients.ColumnExpr("u.id").Scan(ctx, &userIDs); err != nil {
			return fmt.Errorf("load service session reminder recipients: %w", err)
		}
		// 没有收件人时不记录提醒，由之后的扫描在有人工作时补发。
		if len(userIDs) == 0 {
			return nil
		}
		if err := servicestate.Begin(session).Remind(loaded.Now).Save(ctx, tx, w.enqueuer); err != nil {
			return err
		}
		for _, userID := range userIDs {
			if err := notificationtask.EnqueueServiceAttention(ctx, tx, w.enqueuer, session, userID, reason); err != nil {
				return err
			}
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客服处理周期等待超时，已提醒",
			"conversation_id", session.ConversationID,
			"service_session_id", session.ID, "reason", reason, "recipient_count", len(userIDs))
		return nil
	})
}

// reclaim 把负责人超时未回复的周期退回原队列：先锁定排除原负责人后的候选成员、再锁会话并复核，写入退回事件后分配给候选成员，没有候选时留在队列并投递排除原负责人的分配任务；提交后告知原负责人并为其补分配。
func (w *Worker) reclaim(ctx context.Context, snapshot *servermodels.ServiceSession, timeouts domain.ServiceTimeouts) error {
	previousAssigneeID := *snapshot.AssigneeIdentityID
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		member, err := serviceassignment.LockQueueMember(ctx, tx, snapshot.WorkspaceID, snapshot.ID, snapshot.TeamID, previousAssigneeID)
		if err != nil {
			return err
		}
		loaded, err := loadSession(ctx, tx, snapshot.WorkspaceID, snapshot.ID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		conversation, session := loaded.Locked.Conversation, loaded.Session
		// 负责人或队列在读取后已变化时由引起变化的操作负责后续处理。
		if loaded.dueAction(timeouts) != actionReclaim ||
			*session.AssigneeIdentityID != previousAssigneeID || !chatstate.SameTeam(session.TeamID, snapshot.TeamID) {
			return nil
		}
		previous := &servermodels.WorkspaceIdentity{}
		if err := tx.NewSelect().Model(previous).Column("oi.id", "oi.display_name").
			Where("oi.workspace_id = ? AND oi.id = ?", session.WorkspaceID, previousAssigneeID).
			Scan(ctx); err != nil {
			return fmt.Errorf("load reclaimed assignee: %w", err)
		}
		target, err := serviceroute.ServiceSessionQueueTarget(ctx, tx, session)
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
		if _, _, err := chatstate.AppendSystemEvent(ctx, tx, conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
			SystemEventType: &eventType, SystemEventPayload: payload,
		}); err != nil {
			return fmt.Errorf("append service session returned event: %w", err)
		}
		if _, err := chatstate.AppendRequesterStatus(ctx, tx, w.enqueuer, conversation, session, loaded.Locked.Source(), domain.ServiceRequestStatusHandedOff, &target, nil); err != nil {
			return err
		}
		if err := servicestate.Begin(session).Queue(session.TeamID, loaded.Now).Save(ctx, tx, w.enqueuer); err != nil {
			return err
		}
		// 没有候选时投递排除原负责人的分配任务，覆盖回收提交前已完成补分配的成员。
		if member != nil {
			if err := serviceassignment.Assign(ctx, tx, w.enqueuer, loaded.Locked, member); err != nil {
				return err
			}
		} else if err := serviceassignment.EnqueueAssign(ctx, tx, w.enqueuer, serviceassignment.AssignInput{
			WorkspaceID: session.WorkspaceID, ServiceSessionID: session.ID, ExcludeIdentityID: previousAssigneeID,
		}); err != nil {
			return err
		}
		if err := serviceassignment.EnqueueBackfill(ctx, tx, w.enqueuer, serviceassignment.BackfillInput{
			WorkspaceID: session.WorkspaceID, IdentityID: previousAssigneeID, ExcludeServiceSessionID: session.ID,
		}); err != nil {
			return err
		}
		// 读取原负责人账号，提醒其周期已退回队列。
		var previousUserID string
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("u.id").
			Where("u.workspace_id = ? AND u.identity_id = ?", session.WorkspaceID, previousAssigneeID).
			Scan(ctx, &previousUserID); err != nil {
			return fmt.Errorf("load reclaimed assignee user: %w", err)
		}
		if err := notificationtask.EnqueueServiceAttention(ctx, tx, w.enqueuer, session, previousUserID, domain.ServiceAttentionReturned); err != nil {
			return err
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "负责人超时未回复，客服处理周期已退回队列",
			"conversation_id", session.ConversationID,
			"service_session_id", session.ID, "previous_assignee_identity_id", previousAssigneeID,
			"target_kind", target.Kind, "reassigned", member != nil)
		return nil
	})
}

// handleAgentIdle 在事务中锁定周期并复核客户仍未回复 AI 负责人：到达跟进时长时追加一次超时跟进输入，确认请求后到达关单时长时按客户失联关闭周期。
func (w *Worker) handleAgentIdle(ctx context.Context, input ProcessInput, timeouts domain.ServiceTimeouts) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.WorkspaceID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		switch loaded.dueAction(timeouts) {
		case actionFollowUp:
			scheduled, err := w.scheduler.ScheduleCustomerFollowUp(ctx, tx, session)
			if err != nil {
				return err
			}
			slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户超时未回复 AI 员工，处理超时跟进",
				"conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID, "scheduled", scheduled)
		case actionCloseUnresponsive:
			if err := servicesessionaction.CloseAgentServiceSession(ctx, tx, w.enqueuer, loaded.Locked.Conversation, session, loaded.Locked.Source(), domain.ServiceSessionCloseCustomerUnresponsive); err != nil {
				return err
			}
			slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户确认请求后超时未回复，客服处理周期已关闭",
				"conversation_id", session.ConversationID,
				"service_session_id", session.ID, "assignee_identity_id", *session.AssigneeIdentityID)
		}
		return nil
	})
}
