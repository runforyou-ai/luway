//go:build server

// Package servicetimeout 按企业超时设置提醒负责人和队列客服处理等待中的客户，回收负责人超时未回复的客服处理周期，并在客户超时未回复 AI 员工时跟进与关单。
package servicetimeout

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

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
		Join("JOIN service_conversations AS svc ON svc.workspace_id = ss.workspace_id AND svc.id = ss.service_conversation_id").
		Join("JOIN service_policies AS css ON css.workspace_id = ss.workspace_id AND css.audience = svc.audience").
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
