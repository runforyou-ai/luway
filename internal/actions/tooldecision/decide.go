//go:build server

package tooldecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// Decide 由成员处理人确认、批准或拒绝等待裁决的工具调用。
func (a *Action) Decide(ctx context.Context, identity *servermodels.Identity, callID string, approve bool) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := a.lockToolCall(ctx, tx, identity.Workspace.ID, callID)
		if err != nil {
			return err
		}
		var subjectID string
		if err := tx.NewSelect().TableExpr("chat_subjects AS cs").Column("cs.id").
			Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
			Scan(ctx, &subjectID); errors.Is(err, sql.ErrNoRows) {
			return ErrToolCallUnavailable
		} else if err != nil {
			return fmt.Errorf("load member chat subject: %w", err)
		}
		return a.decide(ctx, tx, locked, subjectID, approve)
	})
}

// DecideForCustomer 在调用方已确认客户身份的事务中由客户确认或拒绝会话中等待其确认的工具调用；subjectID 是客户的聊天主体。
func (a *Action) DecideForCustomer(ctx context.Context, tx bun.Tx, workspaceID, conversationID, subjectID, callID string, approve bool) error {
	if !str.IsUUID(callID) {
		return ErrToolCallUnavailable
	}
	locked, err := a.lockToolCall(ctx, tx, workspaceID, callID)
	if err != nil {
		return err
	}
	if locked.Run.ConversationID != conversationID {
		return ErrToolCallUnavailable
	}
	return a.decide(ctx, tx, locked, subjectID, approve)
}

// decide 由处理人主体裁决已锁定的调用：本机 Agent 的权限请求把裁决结果交给电脑；电脑工具调用批准后交给电脑领取，电脑、授权已不允许执行或电脑离线时记为失败；
// 其余调用批准后投递执行；拒绝与失败以结果事件唤醒提交它的 AI 员工；暂停运行等待确认的调用只记下决定并恢复运行。
func (a *Action) decide(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectID string, approve bool) error {
	call := locked.Call
	if call.AssigneeSubjectID == nil || *call.AssigneeSubjectID != subjectID {
		return ErrToolCallUnavailable
	}
	// 截止判断取等待行锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, tx)
	if err != nil {
		return err
	}
	if call.Status != string(domain.AgentToolCallAwaitingDecision) || len(call.Decision) > 0 || call.ExpiresAt == nil || !call.ExpiresAt.After(now) {
		return ErrToolCallDecided
	}
	if call.Source == string(domain.AgentToolSourceLocalAgent) {
		return a.decideLocalAgentPermission(ctx, tx, locked, subjectID, approve)
	}
	if paused(call) {
		decision := agentcontract.CallDecision{Approved: approve, Reason: rejectedByDecider(approve)}
		// 确认执行前按当前配置复核电脑与业务系统授权，已不允许时按拒绝交给模型。
		if approve {
			failure, err := approvedFailure(ctx, tx, a.scopes, locked.Run, call)
			if err != nil {
				return err
			}
			if failure != "" {
				decision = agentcontract.CallDecision{Reason: failure}
			}
		}
		return a.decidePaused(ctx, tx, locked, &subjectID, decision)
	}
	status := domain.AgentToolCallQueued
	if !approve {
		status = domain.AgentToolCallRejected
	}
	update := tx.NewUpdate().Model(call).
		Set("status = ?", status).
		Set("decided_by_subject_id = ?", subjectID).
		Set("decided_at = now()")
	// 批准的电脑操作随即派发到电脑，记下审批请求的串联编号。
	if approve && call.ComputerID != nil {
		update = update.Set("trace_id = ?", support.NilIfZero(logscope.From(ctx).TraceID))
	}
	if _, err := update.WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("decide agent tool call: %w", err)
	}
	call.DecidedBySubjectID = &subjectID
	if !approve {
		return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallRejected, nil, nil)
	}
	workspaceID := call.WorkspaceID
	if call.ComputerID != nil {
		failure, err := checkApprovedComputerCall(ctx, tx, locked.Run, call)
		if err != nil {
			return err
		}
		if failure != "" {
			return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, &failure)
		}
		// 委派本机 Agent 的一轮批准后交给会话中该本机 Agent 的会话，其余电脑操作直接交给电脑领取。
		if call.Operation != nil && call.Operation.Operation.Kind == domain.ComputerOperationLocalAgent {
			var agentID string
			if err := tx.NewSelect().TableExpr("agents AS a").ColumnExpr("a.id::text").
				Where("a.workspace_id = ? AND a.identity_id = ?", workspaceID, locked.Run.AgentIdentityID).
				Scan(ctx, &agentID); err != nil {
				return fmt.Errorf("load approved local agent turn agent: %w", err)
			}
			online, err := localagent.Dispatch(ctx, tx, localagent.Turn{
				WorkspaceID: workspaceID, ConversationID: locked.Run.ConversationID, AgentID: agentID, ComputerID: *call.ComputerID,
				CallID: call.ID, Operation: call.Operation.Operation, From: domain.AgentToolCallQueued,
			})
			if err != nil {
				return err
			}
			if !online {
				return a.settleDecidedToolCall(ctx, tx, locked, domain.AgentToolCallFailed, nil, new(agentcontract.ErrComputerOffline.Error()))
			}
		}
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, *call.ComputerID))
		if err := agentprocess.NotifyDecisionSubjects(ctx, tx, workspaceID, subjectID); err != nil {
			return err
		}
		return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
	}
	if _, err := a.enqueuer.EnqueueIn(ctx, ToolCallExecuteActionName, agentprocess.ToolCallInput{WorkspaceID: workspaceID, ToolCallID: call.ID}, servertask.EnqueueOptions{
		WorkspaceID: workspaceID, MaxAttempts: 3, IdempotencyKey: "agent-tool-call-execute:" + call.ID,
	}); err != nil {
		return fmt.Errorf("enqueue approved agent tool call: %w", err)
	}
	if err := agentprocess.NotifyDecisionSubjects(ctx, tx, workspaceID, subjectID); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline)
}

// paused 判断调用是否暂停运行等待确认：由运行时推进，等待处理人的决定；本机 Agent 的权限请求不属于运行的调用。
func paused(call *servermodels.AgentToolCall) bool {
	return call.Handover == nil && call.Status == string(domain.AgentToolCallAwaitingDecision) && call.Source != string(domain.AgentToolSourceLocalAgent)
}

// rejectedByDecider 返回处理人拒绝时交给模型的原因，批准时为空。
func rejectedByDecider(approve bool) string {
	if approve {
		return ""
	}
	return "发起人拒绝执行这项操作。"
}

// decidePaused 记下暂停运行等待确认的调用的决定与处理人，通知处理人刷新待处理并恢复挂起的运行，运行恢复后据此在运行内执行或拒绝；过期时处理人为空。
func (a *Action) decidePaused(ctx context.Context, tx bun.Tx, locked lockedToolCall, subjectID *string, decision agentcontract.CallDecision) error {
	call := locked.Call
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode agent tool call decision: %w", err)
	}
	if _, err := tx.NewUpdate().Model(call).
		Set("decision = ?::jsonb", string(encoded)).
		Set("decided_by_subject_id = ?", subjectID).
		Set("decided_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("decide paused agent tool call: %w", err)
	}
	if call.AssigneeSubjectID != nil {
		if err := agentprocess.NotifyDecisionSubjects(ctx, tx, call.WorkspaceID, *call.AssigneeSubjectID); err != nil {
			return err
		}
	}
	if err := chatstate.TouchConversation(ctx, tx, locked.Scope.Conversation(), domain.ConversationChangeTimeline); err != nil {
		return err
	}
	return a.scopes.ResumeWaitingRun(ctx, tx, call.WorkspaceID, locked.Run.ID)
}
