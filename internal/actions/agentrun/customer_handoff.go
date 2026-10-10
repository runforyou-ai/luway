//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// customerHandoffAllowed 复核运行仍持有当前开放周期的处理权；转人工不要求 AI 员工仍满足继续执行的资格。
func customerHandoffAllowed(session *servermodels.ServiceSession, run *servermodels.AgentRun) bool {
	return run.ScopeID == session.ID &&
		domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen &&
		session.AssigneeIdentityID != nil && *session.AssigneeIdentityID == run.AgentIdentityID
}

// settleHandoffLane 把原 AI 员工的输入队列结算到锁内 desired_seq，返回结算边界；未认领的输入由人工处理。
func settleHandoffLane(ctx context.Context, db bun.IDB, lane *servermodels.AgentLane) (int64, error) {
	if _, err := db.NewUpdate().Model(lane).
		Set("processed_seq = desired_seq").
		WherePK().Exec(ctx); err != nil {
		return 0, fmt.Errorf("settle handed off agent lane: %w", err)
	}
	return lane.DesiredSeq, nil
}

// completeCustomerHandoff 在同一事务内提交模型或 Runtime 给出的转人工决定：写入事件与通知、改派负责人、结算输入队列并结束运行，返回本次是否写入了完整结果。
func (a *ExecuteAction) completeCustomerHandoff(ctx context.Context, execution executionContext, policy agentRunPolicy, result agentruntime.RunResult, usage []byte) (bool, error) {
	suppressed, completed := false, false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		resolved, err := servicehandoff.ResolveRoute(ctx, tx, execution.Run.WorkspaceID, execution.Run.ConversationID, execution.Run.ScopeID, execution.Run.AgentIdentityID, result.Decision.CategoryID)
		if err != nil {
			return err
		}
		locked, err := lockAgentRun(ctx, tx, policy, &execution.Run, false)
		if err != nil {
			return fmt.Errorf("lock agent run for handoff: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			return nil
		}
		if !customerHandoffAllowed(policyContext.ServiceSession, run) {
			suppressed = true
			if err := suppressCustomerRun(ctx, tx, run, policyContext.ServiceSession); err != nil {
				return err
			}
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		if run.Status != string(domain.AgentRunStatusRunning) || run.InputEndSeq == nil ||
			*run.InputEndSeq != result.EndSeq || run.InputStartSeq != lane.ProcessedSeq+1 {
			return errors.New("agent run handoff boundary is inconsistent")
		}
		message, err := servicehandoff.Apply(ctx, tx, a.enqueuer, a.emailSender, servicehandoff.Handoff{
			Conversation: policyContext.Conversation, Session: policyContext.ServiceSession, Source: policyContext.ServiceSource, DeliveryRoute: policyContext.DeliveryRoute,
			Channel: resolved.Channel, AgentIdentityID: run.AgentIdentityID, Queue: resolved.Queue, Member: resolved.Member,
			NoticeKey: "agent:" + run.ID, EventKey: "agent:" + run.ID + ":handoff-event",
			Reason: result.Decision.Reason, ReasonText: result.Decision.ReasonText, Category: resolved.Category, AgentRunID: &run.ID,
		})
		if err != nil {
			return err
		}
		if err := syncProcess(ctx, tx, run, result.Blocks, result.Calls); err != nil {
			return err
		}
		settledSeq, err := settleHandoffLane(ctx, tx, lane)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusSucceeded).
			Set("outcome = ?", domain.AgentRunOutcomeHandoff).
			Set("outcome_reason = ?", result.Decision.Reason).
			Set("response_message_id = ?", message.ID).
			Set("handoff_settled_seq = ?", settledSeq).
			Set("usage = ?::jsonb", string(usage)).
			Set("last_error = NULL").
			Set("error_code = NULL").
			Set("completed_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("complete handed off agent run: %w", err)
		}
		completed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if suppressed {
		slog.WarnContext(ctx, "客户 Agent 迟到的转人工结果已抑制", "agent_run_id", execution.Run.ID, "conversation_id", execution.Run.ConversationID)
	}
	if completed {
		slog.InfoContext(ctx, "客户 Agent 转交人工", "agent_run_id", execution.Run.ID, "conversation_id", execution.Run.ConversationID,
			"service_session_id", execution.Run.ScopeID, "reason", result.Decision.Reason)
	}
	return completed, nil
}

// failCustomerRun 把客服运行失败收敛为转人工：绑定失败输入，写入内部错误消息、转人工事件与对客通知，改派负责人并结算输入队列。
func (a *ExecuteAction) failCustomerRun(ctx context.Context, initial *servermodels.AgentRun, policy agentRunPolicy, lastError string, reason domain.AgentHandoffReason) (bool, error) {
	terminal := false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		resolved, err := servicehandoff.ResolveRoute(ctx, tx, initial.WorkspaceID, initial.ConversationID, initial.ScopeID, initial.AgentIdentityID, "")
		if err != nil {
			return err
		}
		locked, err := lockAgentRun(ctx, tx, policy, initial, false)
		if err != nil {
			return fmt.Errorf("lock agent run for failure: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			terminal = true
			return nil
		}
		if !customerHandoffAllowed(policyContext.ServiceSession, run) {
			terminal = true
			if err := suppressCustomerRun(ctx, tx, run, policyContext.ServiceSession); err != nil {
				return err
			}
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		// 挂起或已由其他任务执行的运行，失败收尾不改变它。
		if run.Status == string(domain.AgentRunStatusWaiting) || ownedByOtherTask(ctx, run) {
			terminal = true
			return nil
		}
		if run.Status != string(domain.AgentRunStatusQueued) && run.Status != string(domain.AgentRunStatusRunning) {
			return fmt.Errorf("cannot fail agent run in status %q", run.Status)
		}
		failureEnd := support.DerefOr(run.InputEndSeq, run.InputStartSeq)
		if run.InputStartSeq != lane.ProcessedSeq+1 || failureEnd < run.InputStartSeq || failureEnd > lane.DesiredSeq {
			return errors.New("agent run failure boundary is inconsistent")
		}
		failedSeqs, err := claimLaneInputs(ctx, tx, run, lane.ProcessedSeq, failureEnd)
		if err != nil {
			return err
		}
		if int64(len(failedSeqs)) != failureEnd-lane.ProcessedSeq {
			return errors.New("failed agent input sequence is not contiguous")
		}
		participantID, err := agentmessage.EnsureCustomerParticipant(ctx, tx, run.WorkspaceID, run.ConversationID, run.AgentIdentityID)
		if err != nil {
			return err
		}
		// 运行错误只作为成员可见的内部消息写入，不投递、不计入首响。
		errorKey := "agent:" + run.ID + ":error"
		if _, _, err := agentmessage.Append(ctx, tx, a.enqueuer, policyContext.Conversation, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: run.WorkspaceID, ConversationID: run.ConversationID,
			ServiceSessionID: &policyContext.ServiceSession.ID, SenderParticipantID: &participantID,
			Type: string(domain.MessageTypeAgentError), Visibility: string(domain.MessageVisibilityInternal), IdempotencyKey: &errorKey,
		}); err != nil {
			return err
		}
		message, err := servicehandoff.Apply(ctx, tx, a.enqueuer, a.emailSender, servicehandoff.Handoff{
			Conversation: policyContext.Conversation, Session: policyContext.ServiceSession, Source: policyContext.ServiceSource, DeliveryRoute: policyContext.DeliveryRoute,
			Channel: resolved.Channel, AgentIdentityID: run.AgentIdentityID, Queue: resolved.Queue, Member: resolved.Member,
			NoticeKey: "agent:" + run.ID, EventKey: "agent:" + run.ID + ":handoff-event",
			Reason: reason, ReasonText: lastError, AgentRunID: &run.ID,
		})
		if err != nil {
			return err
		}
		settledSeq, err := settleHandoffLane(ctx, tx, lane)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusFailed).
			Set("outcome = ?", domain.AgentRunOutcomeHandoff).
			Set("outcome_reason = ?", reason).
			Set("response_message_id = ?", message.ID).
			Set("input_end_seq = ?", failureEnd).
			Set("handoff_settled_seq = ?", settledSeq).
			Set("last_error = ?", lastError).
			Set("error_code = NULL").
			Set("completed_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("fail handed off agent run: %w", err)
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.WorkspaceID, run.ID); err != nil {
			return err
		}
		return nil
	})
	return terminal, err
}
