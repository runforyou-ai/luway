//go:build server

// Package tooldecision 实现需要确认、审批或核对的工具调用的状态机：提交处理人与截止时间、成员与客户裁决、批准后执行、过期、取消后的结果事件、
// 渠道确认提醒、本机 Agent 的权限请求，以及派发到电脑的调用有结果后通知负责人并唤醒提交它的 AI 员工或等待结果的运行。
// 运行所属会话的锁定、事件输入的调度与挂起运行的恢复由注入的 RunScopes 提供。
package tooldecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

const (
	// ToolCallExecuteActionName 执行已批准的工具调用。
	ToolCallExecuteActionName = "agent.tool_call.execute"
	// ToolCallExpireActionName 把截止时仍未确认或审批的工具调用记为过期。
	ToolCallExpireActionName = "agent.tool_call.expire"
)

// Submit 在写入等待确认或审批的调用的事务中指定处理人与截止时间，在会话中写入待处理事件，通知成员处理人并安排过期，渠道会话中成员的确认另经渠道提醒；
// 确认由发起人处理，审批由 AI 员工的负责人处理，找不到处理人时返回 agentcontract.ErrDecisionUnavailable；调用已提交过时不做任何事。
func Submit(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, run *servermodels.AgentRun, call agentcontract.ToolCall) error {
	var assignee Decider
	var err error
	if call.Intervention == domain.ToolInterventionApproval {
		assignee, err = LoadApprover(ctx, tx, run.WorkspaceID, run.AgentIdentityID)
	} else {
		assignee, err = loadInitiator(ctx, tx, run)
	}
	if err != nil {
		return err
	}
	if assignee.UserID == "" && assignee.SubjectID == "" {
		return agentcontract.ErrDecisionUnavailable
	}
	// 尚无聊天主体的负责人在提交时建立主体。
	if assignee.SubjectID == "" {
		subject, err := chatstate.EnsureSubject(ctx, tx, run.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, assignee.IdentityID, uuid.NewV7().String())
		if err != nil {
			return err
		}
		assignee.SubjectID = subject.ID
	}
	// 处理期限与到期任务的执行时间都从本事务的数据库时刻起算。
	updated, err := tx.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
		Set("assignee_subject_id = ?", assignee.SubjectID).
		Set("expires_at = now() + make_interval(secs => ?)", domain.ToolDecisionTimeout.Seconds()).
		Where("id = ? AND workspace_id = ? AND status = ? AND assignee_subject_id IS NULL", call.ID, run.WorkspaceID, domain.AgentToolCallAwaitingDecision).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("assign agent tool call decision: %w", err)
	}
	if affected, err := updated.RowsAffected(); err != nil || affected == 0 {
		return err
	}
	if _, _, err := appendToolCallEvent(ctx, tx, enqueuer, conversation, run, call.ID, call.Intervention, domain.ConversationSystemEventAgentToolCallPending); err != nil {
		return err
	}
	if assignee.UserID != "" {
		if err := notificationtask.EnqueueToolDecision(ctx, tx, enqueuer, run.WorkspaceID, assignee.UserID, call.ID); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.UserToolDecisionsChanged(run.WorkspaceID, assignee.UserID))
	}
	// 渠道会话中的发起成员不在应用中对话，确认请求另经渠道发送处理链接。
	if call.Intervention == domain.ToolInterventionConfirmation && assignee.UserID != "" && conversation.Type == string(domain.ConversationTypeChannel) {
		if err := enqueueToolDecisionReminder(ctx, tx, enqueuer, run.WorkspaceID, call.ID); err != nil {
			return err
		}
	}
	if _, err := enqueuer.EnqueueIn(ctx, ToolCallExpireActionName, agentprocess.ToolCallInput{WorkspaceID: run.WorkspaceID, ToolCallID: call.ID}, servertask.EnqueueOptions{
		WorkspaceID: run.WorkspaceID, MaxAttempts: 3, IdempotencyKey: "agent-tool-call-expire:" + call.ID, Delay: domain.ToolDecisionTimeout,
	}); err != nil {
		return fmt.Errorf("enqueue agent tool call expiry: %w", err)
	}
	return nil
}

// appendToolCallEvent 在调用所属会话中写入工具调用的待处理或结果事件，同一调用的同类事件只写入一次：
// 服务会话中需要审批的调用只对处理方可见，其余对会话各方可见。
func appendToolCallEvent(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, run *servermodels.AgentRun, callID string,
	intervention domain.ToolIntervention, eventType domain.ConversationSystemEventType) (*servermodels.Message, bool, error) {
	var agentName string
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).Column("display_name").
		Where("oi.workspace_id = ? AND oi.id = ?", run.WorkspaceID, run.AgentIdentityID).Scan(ctx, &agentName); err != nil {
		return nil, false, fmt.Errorf("load agent name for tool call event: %w", err)
	}
	payload, err := json.Marshal(domain.AgentToolCallEvent{
		Actor: domain.ConversationEventParticipant{IdentityID: run.AgentIdentityID, DisplayName: agentName}, ToolCallID: callID,
	})
	if err != nil {
		return nil, false, fmt.Errorf("encode tool call event: %w", err)
	}
	message := &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: run.WorkspaceID, ConversationID: run.ConversationID,
		Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityShared),
		SystemEventPayload: payload,
	}
	typeName, key := string(eventType), string(eventType)+":"+callID
	message.SystemEventType, message.IdempotencyKey = &typeName, &key
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		message.ServiceSessionID = &run.ScopeID
		if intervention == domain.ToolInterventionApproval {
			message.Visibility = string(domain.MessageVisibilityInternal)
		}
	}
	return agentmessage.Append(ctx, db, enqueuer, conversation, message)
}

// lockedToolCall 是在会话锁内锁定的工具调用、其所属运行与按运行策略锁定的会话上下文。
type lockedToolCall struct {
	Call  *servermodels.AgentToolCall
	Run   *servermodels.AgentRun
	Scope RunScope
}

// lockToolCall 按运行策略锁定调用所属会话后锁定工具调用，调用不存在时返回 ErrToolCallUnavailable。
func (a *Action) lockToolCall(ctx context.Context, tx bun.Tx, workspaceID, callID string) (lockedToolCall, error) {
	run := &servermodels.AgentRun{}
	err := tx.NewSelect().Model(run).
		Join("JOIN agent_tool_calls AS atc ON atc.agent_run_id = agr.id AND atc.workspace_id = agr.workspace_id").
		Where("atc.workspace_id = ? AND atc.id = ?", workspaceID, callID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedToolCall{}, ErrToolCallUnavailable
	}
	if err != nil {
		return lockedToolCall{}, fmt.Errorf("load agent tool call run: %w", err)
	}
	scope, err := a.scopes.Lock(ctx, tx, run)
	if err != nil {
		return lockedToolCall{}, err
	}
	call := &servermodels.AgentToolCall{}
	if err := tx.NewSelect().Model(call).Where("atc.workspace_id = ? AND atc.id = ?", workspaceID, callID).For("UPDATE").Scan(ctx); err != nil {
		return lockedToolCall{}, fmt.Errorf("lock agent tool call: %w", err)
	}
	return lockedToolCall{Call: call, Run: run, Scope: scope}, nil
}

// resolveToolCall 在调用已有结果的事务中写入结果事件、唤醒提交它的 AI 员工并通知成员处理人刷新待处理列表；
// 结果事件作为事件输入进入原执行范围，执行范围已失去执行资格时只保留事件。
func (a *Action) resolveToolCall(ctx context.Context, tx bun.Tx, locked lockedToolCall) error {
	call, run := locked.Call, locked.Run
	message, inserted, err := appendToolCallEvent(ctx, tx, a.enqueuer, locked.Scope.Conversation(), run, call.ID,
		domain.ToolIntervention(*call.Intervention), domain.ConversationSystemEventAgentToolCallResolved)
	if err != nil || !inserted {
		return err
	}
	if call.AssigneeSubjectID != nil {
		if err := agentprocess.NotifyDecisionSubjects(ctx, tx, run.WorkspaceID, *call.AssigneeSubjectID); err != nil {
			return err
		}
	}
	return locked.Scope.Wake(ctx, tx, run, message.ID)
}

// settleDecidedToolCall 写入工具调用的结束状态、结果或错误，并以结果事件唤醒提交它的 AI 员工；本机 Agent 的权限请求只通知电脑领取结果。
func (a *Action) settleDecidedToolCall(ctx context.Context, tx bun.Tx, locked lockedToolCall, status domain.AgentToolCallStatus, result, failure *string) error {
	locked.Call.Status, locked.Call.Result, locked.Call.Error = string(status), result, failure
	if _, err := tx.NewUpdate().Model(locked.Call).
		Set("status = ?", status).
		Set("result = ?", result).
		Set("error = ?", failure).
		Set("completed_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("settle agent tool call: %w", err)
	}
	if locked.Call.Source == string(domain.AgentToolSourceLocalAgent) {
		return settleLocalAgentPermission(ctx, tx, locked, support.Deref(locked.Call.AssigneeSubjectID))
	}
	return a.resolveToolCall(ctx, tx, locked)
}

// Decider 是确认或审批的处理人：聊天主体，以及处理人是成员时的用户编号与工作区身份编号，客户的用户编号为空。
type Decider struct {
	SubjectID  string
	UserID     string
	IdentityID string
}

// LoadApprover 返回 AI 员工的在职负责人，没有时为空；负责人尚无聊天主体时主体编号为空。
func LoadApprover(ctx context.Context, db bun.IDB, workspaceID, agentIdentityID string) (Decider, error) {
	var approver Decider
	err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("u.id::text AS user_id, u.identity_id::text AS identity_id, COALESCE(cs.id::text, '') AS subject_id").
		Join("JOIN users AS u ON u.id = a.responsible_user_id AND u.workspace_id = a.workspace_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.workspace_id = u.workspace_id AND cs.kind = ? AND cs.source_id = u.identity_id", domain.ChatSubjectKindWorkspaceIdentity).
		Where("a.workspace_id = ? AND a.identity_id = ?", workspaceID, agentIdentityID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Scan(ctx, &approver.UserID, &approver.IdentityID, &approver.SubjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Decider{}, nil
	}
	if err != nil {
		return Decider{}, fmt.Errorf("load agent approver: %w", err)
	}
	return approver, nil
}

// loadInitiator 返回需要确认的调用的发起人：服务客户的周期为会话的客户；其余执行范围为运行所在输入队列中最近一条已认领的、来自在职成员的输入的发送者，
// 事件唤醒的运行沿用此前的发起人；没有时为空。
func loadInitiator(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (Decider, error) {
	if domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
		if err != nil {
			return Decider{}, err
		}
		if domain.ServiceAudience(service.Audience) == domain.ServiceAudienceCustomer {
			return Decider{SubjectID: service.RequesterSubjectID}, nil
		}
	}
	var initiator Decider
	err := db.NewSelect().TableExpr("agent_inputs AS ai").
		ColumnExpr("ai.source_subject_id::text, u.id::text, u.identity_id::text").
		Join("JOIN chat_subjects AS cs ON cs.id = ai.source_subject_id AND cs.workspace_id = ai.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN users AS u ON u.identity_id = cs.source_id AND u.workspace_id = cs.workspace_id").
		Where("ai.workspace_id = ? AND ai.lane_id = ? AND ai.agent_run_id IS NOT NULL", run.WorkspaceID, run.LaneID).
		Where("u.status = ?", domain.IdentityStatusActive).
		OrderExpr("ai.input_seq DESC").Limit(1).
		Scan(ctx, &initiator.SubjectID, &initiator.UserID, &initiator.IdentityID)
	if errors.Is(err, sql.ErrNoRows) {
		return Decider{}, nil
	}
	if err != nil {
		return Decider{}, fmt.Errorf("load tool call initiator: %w", err)
	}
	return initiator, nil
}
