//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// fail 按运行策略取消失效运行或标记失败：客服运行转交人工，其他运行写入错误消息、记录错误码并为剩余输入补建下一次运行；policy 为空时按运行解析，code 为空表示没有稳定错误码。
func (a *ExecuteAction) fail(ctx context.Context, runID string, policy agentRunPolicy, runErr error, code domain.AgentRunErrorCode) (bool, error) {
	// 限制持久化错误详情长度。
	message := "agent run failed"
	if runErr != nil {
		message = str.Substr(runErr.Error(), 0, agentRunErrorMaxRunes)
	}
	initial := &servermodels.AgentRun{}
	if err := a.db.NewSelect().Model(initial).Where("agr.id = ?", runID).Scan(ctx); err != nil {
		return false, err
	}
	// 挂起的运行没有执行中的任务，失败收尾不改变它。
	if agentRunStatusTerminal(initial.Status) || initial.Status == string(domain.AgentRunStatusWaiting) {
		return true, nil
	}
	if policy == nil {
		resolved, err := a.policyForRun(ctx, initial)
		if err != nil {
			return false, err
		}
		policy = resolved
	}
	if domain.AgentExecutionScopeKind(initial.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		reason := domain.AgentHandoffReasonRuntimeFailed
		if errors.Is(runErr, context.DeadlineExceeded) {
			reason = domain.AgentHandoffReasonTimeout
		}
		return a.failCustomerRun(ctx, initial, policy, message, reason)
	}
	terminal := false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, initial, false)
		if err != nil {
			return fmt.Errorf("lock agent run for failure: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			terminal = true
			return nil
		}
		allowed, err := policy.prepareLocked(ctx, tx, policyContext, run)
		if err != nil {
			return err
		}
		if !allowed {
			terminal = true
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
		messageID := uuid.NewV7().String()
		if err := policy.persistMessage(ctx, tx, policyContext, run, messageID, domain.MessageTypeAgentError, ""); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusFailed).
			Set("response_message_id = ?", messageID).
			Set("input_end_seq = ?", failureEnd).
			Set("last_error = ?", message).
			Set("error_code = NULLIF(?, '')", code).
			Set("completed_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.WorkspaceID, run.ID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", failureEnd).
			WherePK().Exec(ctx); err != nil {
			return err
		}
		return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
	})
	return terminal, err
}

// FinalizeFailure 在任务达到最终失败时收敛 Agent 业务运行。
func (a *ExecuteAction) FinalizeFailure(ctx context.Context, input RunInput, runErr error) error {
	_, err := a.fail(ctx, input.RunID, nil, runErr, "")
	return err
}
