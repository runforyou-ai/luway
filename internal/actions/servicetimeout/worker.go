//go:build server

// Package servicetimeout 按企业超时设置提醒负责人和队列客服处理等待中的客户，回收负责人超时未回复的客服处理周期，并在客户超时未回复 AI 员工时跟进与关单。
package servicetimeout

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/servicepolicy"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
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
	loaded, err := loadSession(ctx, w.db, input.WorkspaceID, input.ServiceSessionID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch loaded.dueAction() {
	case actionReclaim:
		return w.reclaim(ctx, loaded.Session)
	case actionRemindAssignee, actionRemindQueue:
		return w.remind(ctx, input)
	case actionFollowUp, actionCloseUnresponsive:
		return w.handleAgentIdle(ctx, input)
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
	Timeouts     domain.ServiceTimeouts
}

// dueAction 按读取到的周期状态与读取时的数据库时刻判断应执行的动作。
func (l loadedSession) dueAction() timeoutAction {
	return dueAction(l.Session, l.AssigneeType, l.AgentIdle, l.Timeouts, l.Now)
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
	policy, err := servicepolicy.ForSession(ctx, db, workspaceID, serviceSessionID)
	if err != nil {
		return loadedSession{}, err
	}
	loaded.Timeouts = servicepolicy.Timeouts(policy)
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
