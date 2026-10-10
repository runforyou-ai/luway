//go:build server

// Package servicestate 是服务周期（service_sessions）的唯一写入入口：流转在已锁定的周期上以纯函数修改内存值并记录变化的列，由 Save 以一条 UPDATE 落库并处理流转绑定的副作用；小结、评价与访客上下文等独立字段由具名方法按字段归属写入。
package servicestate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ErrDataInvariant 表示服务周期的持久字段互相矛盾。
var ErrDataInvariant = errors.New("conversation data invariant violated")

// ErrServiceSessionNotFound 表示企业中没有指定的服务周期。
var ErrServiceSessionNotFound = errors.New("service session not found")

// Transition 汇总对一个已锁定周期的一次流转：在内存中修改周期并记录变化的列与需要执行的副作用。
type Transition struct {
	session             *servermodels.ServiceSession
	columns             []string
	cancelToolDecisions bool
}

// Begin 在调用方持有周期锁的事务中开始对该周期的一次流转。
func Begin(session *servermodels.ServiceSession) *Transition {
	return &Transition{session: session}
}

// set 记录变化的列，同一列只记录一次。
func (t *Transition) set(columns ...string) {
	for _, column := range columns {
		if !slices.Contains(t.columns, column) {
			t.columns = append(t.columns, column)
		}
	}
}

// requestHuman 记录周期首次需要真人的时间，已记录时保持不变。
func (t *Transition) requestHuman(now time.Time) {
	if t.session.HumanRequestedAt == nil {
		t.session.HumanRequestedAt = new(now)
		t.set("human_requested_at")
	}
}

// assign 把周期交给负责人：首次负责时记录负责时间，负责人接手时间记为 now，清空排队与提醒时间，所属队列不变，撤销待确认的工具调用。
func (t *Transition) assign(identityID string, now time.Time) {
	s := t.session
	s.AssigneeIdentityID = &identityID
	if s.AssignedAt == nil {
		s.AssignedAt = new(now)
		t.set("assigned_at")
	}
	s.AssigneeAssignedAt, s.QueuedAt, s.RemindedAt = new(now), nil, nil
	t.set("assignee_identity_id", "assignee_assigned_at", "queued_at", "reminded_at")
	t.cancelToolDecisions = true
}

// Assign 把周期交给真人成员：按 assign 写入负责人，并记录周期首次需要真人与真人首次负责的时间。
func (t *Transition) Assign(identityID string, now time.Time) *Transition {
	t.requestHuman(now)
	t.assign(identityID, now)
	if t.session.HumanAssignedAt == nil {
		t.session.HumanAssignedAt = new(now)
		t.set("human_assigned_at")
	}
	return t
}

// AssignAgent 把周期交给 AI 员工：按 assign 写入负责人，需要真人、真人负责与真人首响的记录保持不变；周期首次由 AI 员工负责时记为其接待。
func (t *Transition) AssignAgent(agentIdentityID string, now time.Time) *Transition {
	s := t.session
	t.assign(agentIdentityID, now)
	if s.AgentIdentityID == nil {
		s.AgentIdentityID = &agentIdentityID
		t.set("agent_identity_id")
	}
	return t
}

// Queue 把周期退回 teamID 对应的队列：清空负责人与接手时间，从 now 起计入队列并清空提醒时间，记录周期需要真人的时间，撤销待确认的工具调用；teamID 为空表示公共队列，客户等待起点不变。
func (t *Transition) Queue(teamID *string, now time.Time) *Transition {
	s := t.session
	t.requestHuman(now)
	s.AssigneeIdentityID, s.AssigneeAssignedAt, s.TeamID = nil, nil, teamID
	s.QueuedAt, s.RemindedAt = new(now), nil
	t.set("assignee_identity_id", "assignee_assigned_at", "team_id", "queued_at", "reminded_at")
	t.cancelToolDecisions = true
	return t
}

// Handoff 把 AI 员工负责的周期转入人工队列：按 Queue 退回 teamID 对应的队列，客户等待起点写为交接前的 awaitingReplySince，交接前客户不在等待时以 now 为等待起点；categoryID 不为空时记为周期的咨询分类。
func (t *Transition) Handoff(teamID *string, awaitingReplySince *time.Time, categoryID *string, now time.Time) *Transition {
	t.Queue(teamID, now)
	if awaitingReplySince == nil {
		awaitingReplySince = new(now)
	}
	t.session.AwaitingReplySince = awaitingReplySince
	t.set("awaiting_reply_since")
	if categoryID != nil {
		t.session.CategoryID = categoryID
		t.set("category_id")
	}
	return t
}

// Close 关闭开放周期：记录关闭时间、关闭人与结束方式，清空客户等待、排队、提醒与确认请求时间，负责人与所属队列保持不变，撤销待确认的工具调用。
func (t *Transition) Close(closedByIdentityID string, reason domain.ServiceSessionCloseReason, now time.Time) *Transition {
	s := t.session
	closeReason := string(reason)
	s.Status, s.StatusChangedAt, s.ClosedAt = string(domain.ServiceSessionStatusClosed), now, new(now)
	s.ClosedByIdentityID, s.CloseReason = &closedByIdentityID, &closeReason
	s.AwaitingReplySince, s.QueuedAt, s.RemindedAt, s.ResolutionRequestedAt = nil, nil, nil, nil
	t.set("status", "status_changed_at", "closed_at", "closed_by_identity_id", "close_reason",
		"awaiting_reply_since", "queued_at", "reminded_at", "resolution_requested_at")
	t.cancelToolDecisions = true
	return t
}

// Reopen 重新打开已关闭的周期并清空关闭记录，所属队列保持不变，客户等待起点由客户下一条消息设置；调用方随后指定负责人。
func (t *Transition) Reopen(now time.Time) *Transition {
	s := t.session
	s.Status, s.StatusChangedAt = string(domain.ServiceSessionStatusOpen), now
	s.ClosedAt, s.ClosedByIdentityID, s.CloseReason = nil, nil, nil
	t.set("status", "status_changed_at", "closed_at", "closed_by_identity_id", "close_reason")
	return t
}

// RecordMessage 按开放周期中新追加的共享对话消息推进周期最后消息：发起人的消息开始或延续客户等待，处理方的消息结束等待；等待起点变化时清空本轮提醒时间，并清空 AI 请求确认解决的时间。已关闭的周期保持不变。
func (t *Transition) RecordMessage(messageID string, originatedAt time.Time, fromRequester bool) *Transition {
	s := t.session
	if domain.ServiceSessionStatus(s.Status) != domain.ServiceSessionStatusOpen {
		return t
	}
	s.LastMessageID, s.LastMessageAt = messageID, originatedAt
	switch {
	case !fromRequester:
		s.AwaitingReplySince, s.RemindedAt = nil, nil
	case s.AwaitingReplySince == nil:
		s.AwaitingReplySince, s.RemindedAt = new(originatedAt), nil
	}
	s.ResolutionRequestedAt = nil
	t.set("last_message_id", "last_message_at", "awaiting_reply_since", "reminded_at", "resolution_requested_at")
	return t
}

// ResumeAwaiting 把客户等待起点与本轮提醒时间写回处理方对客通知之前的值，用于不结束客户等待的对客通知。
func (t *Transition) ResumeAwaiting(awaitingReplySince, remindedAt *time.Time) *Transition {
	t.session.AwaitingReplySince, t.session.RemindedAt = awaitingReplySince, remindedAt
	t.set("awaiting_reply_since", "reminded_at")
	return t
}

// RecordStaffReply 记录成员对客回复：首次回复记为周期首响；真人首次回复时记录真人首响时间，并按 hours 计算自首次需要真人起的工作时间用时，其间没有经过工作时间时不记用时。周期没有需要真人的时间时返回 ErrDataInvariant。
func (t *Transition) RecordStaffReply(repliedAt time.Time, hours domain.BusinessHours) error {
	s := t.session
	if s.HumanFirstResponseAt == nil {
		if s.HumanRequestedAt == nil {
			return ErrDataInvariant
		}
		var seconds *int
		if working := hours.WorkingDuration(*s.HumanRequestedAt, repliedAt); working > 0 {
			seconds = new(int(working / time.Second))
		}
		s.HumanFirstResponseAt, s.HumanFirstResponseSec = new(repliedAt), seconds
		t.set("human_first_response_at", "human_first_response_seconds")
	}
	if s.FirstResponseAt == nil {
		s.FirstResponseAt = new(repliedAt)
		t.set("first_response_at")
	}
	return nil
}

// RecordAgentReply 在 AI 员工的对客文本成为开放周期最后消息时记录周期首响，已记录时保持不变。
func (t *Transition) RecordAgentReply(messageID string, repliedAt time.Time) *Transition {
	s := t.session
	if domain.ServiceSessionStatus(s.Status) == domain.ServiceSessionStatusOpen && s.LastMessageID == messageID && s.FirstResponseAt == nil {
		s.FirstResponseAt = new(repliedAt)
		t.set("first_response_at")
	}
	return t
}

// RequestResolution 在 AI 员工请客户确认解决的消息仍是开放周期最后消息时记录确认请求时间。
func (t *Transition) RequestResolution(messageID string, now time.Time) *Transition {
	s := t.session
	if domain.ServiceSessionStatus(s.Status) == domain.ServiceSessionStatusOpen && s.LastMessageID == messageID {
		s.ResolutionRequestedAt = new(now)
		t.set("resolution_requested_at")
	}
	return t
}

// Remind 记录本轮等待提醒已发出的时间。
func (t *Transition) Remind(now time.Time) *Transition {
	t.session.RemindedAt = new(now)
	t.set("reminded_at")
	return t
}

// Save 在调用方持有周期锁的事务中以一条 UPDATE 写入本次流转变化的列并推进 updated_at，随后撤销流转要求撤销的待确认工具调用；没有变化时不写入。
func (t *Transition) Save(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer) error {
	if len(t.columns) > 0 {
		if err := update(ctx, db, t.session, t.columns...); err != nil {
			return fmt.Errorf("save service session transition: %w", err)
		}
	}
	if t.cancelToolDecisions {
		return agentprocess.CancelServiceSessionToolDecisions(ctx, db, enqueuer, t.session.WorkspaceID, t.session.ID)
	}
	return nil
}

// update 以一条 UPDATE 写入周期的指定列，内存中的 updated_at 同步为数据库写入值。
func update(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, columns ...string) error {
	_, err := db.NewUpdate().Model(session).
		Column(columns...).
		WherePK().Where("workspace_id = ?", session.WorkspaceID).
		Returning("updated_at").
		Exec(ctx)
	return err
}

// RecordStaffReply 在调用方持有周期锁的事务中记录成员对客回复的周期首响与真人首响，真人首响用时按当前客服工作时间计算；调用方保证周期已由真人负责。
func RecordStaffReply(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, repliedAt time.Time) error {
	var hours domain.BusinessHours
	if session.HumanFirstResponseAt == nil {
		loaded, err := customerservice.LoadBusinessHours(ctx, db, session.WorkspaceID)
		if err != nil {
			return err
		}
		hours = loaded
	}
	transition := Begin(session)
	if err := transition.RecordStaffReply(repliedAt, hours); err != nil {
		return err
	}
	return transition.Save(ctx, db, nil)
}

// RecordMessage 在追加消息的事务中按新追加的共享对话消息推进其开放周期，周期已关闭时不写入；locked 为调用方已锁定的该周期时按 Transition.RecordMessage 就地更新内存值并落库，为空时以一条与之等价的 UPDATE 直接写入，行锁位置与该 UPDATE 相同。
func RecordMessage(ctx context.Context, db bun.IDB, locked *servermodels.ServiceSession, message *servermodels.Message) error {
	fromRequester := db.NewSelect().TableExpr("conversation_participants AS cp").ColumnExpr("1").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cp.workspace_id AND svc.conversation_id = cp.conversation_id AND svc.requester_subject_id = cp.subject_id").
		Where("cp.workspace_id = ? AND cp.id = ?", message.WorkspaceID, message.SenderParticipantID)
	if locked != nil && locked.ID == *message.ServiceSessionID {
		if domain.ServiceSessionStatus(locked.Status) != domain.ServiceSessionStatusOpen {
			return nil
		}
		requester, err := fromRequester.Exists(ctx)
		if err != nil {
			return fmt.Errorf("check service message requester: %w", err)
		}
		return Begin(locked).RecordMessage(message.ID, message.OriginatedAt, requester).Save(ctx, db, nil)
	}
	// 与 Transition.RecordMessage 相同的规则：发起人的消息开始或延续等待，处理方的消息结束等待，等待起点变化时清空本轮提醒，并清空确认请求时间。
	if _, err := db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).
		Set("last_message_id = ?", message.ID).
		Set("last_message_at = ?", message.OriginatedAt).
		Set("awaiting_reply_since = CASE WHEN EXISTS (?) THEN COALESCE(awaiting_reply_since, ?) ELSE NULL END", fromRequester, message.OriginatedAt).
		Set("reminded_at = CASE WHEN EXISTS (?) AND awaiting_reply_since IS NOT NULL THEN reminded_at ELSE NULL END", fromRequester).
		Set("resolution_requested_at = NULL").
		Where("workspace_id = ? AND conversation_id = ? AND id = ?", message.WorkspaceID, message.ConversationID, *message.ServiceSessionID).
		Where("status = ?", domain.ServiceSessionStatusOpen).
		Exec(ctx); err != nil {
		return fmt.Errorf("record service session message: %w", err)
	}
	return nil
}
