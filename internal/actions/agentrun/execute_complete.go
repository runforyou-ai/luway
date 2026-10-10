//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"uuid"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// agentResultMessage 构造 Run 的主结果消息，幂等键为 agent:<run_id>。
func agentResultMessage(run *servermodels.AgentRun, messageID, participantID string, messageType domain.MessageType, content string, serviceSessionID *string) *servermodels.Message {
	return &servermodels.Message{
		ID: messageID, WorkspaceID: run.WorkspaceID, ConversationID: run.ConversationID,
		ServiceSessionID: serviceSessionID, SenderParticipantID: &participantID,
		Type: string(messageType), Body: content, IdempotencyKey: new("agent:" + run.ID),
	}
}

// complete 按运行策略抑制失效结果或原子写入回复并推进消费序号，返回本次是否写入了完整结果与过程内容。
func (a *ExecuteAction) complete(ctx context.Context, execution executionContext, policy agentRunPolicy, result agentruntime.RunResult) (bool, error) {
	content := strings.TrimSpace(result.Content)
	handoff := result.Decision.Kind == domain.AgentRunOutcomeHandoff
	if handoff && domain.AgentExecutionScopeKind(execution.Run.ScopeKind) != domain.AgentExecutionScopeServiceSession {
		return false, errors.New("agent runtime returned a handoff outside customer service")
	}
	if (content == "" && !handoff) || result.EndSeq <= 0 {
		return false, errors.New("agent runtime returned an invalid result")
	}
	usage, err := json.Marshal(result.Usage)
	if err != nil {
		return false, fmt.Errorf("encode agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(result.Plan)
	if err != nil {
		return false, err
	}
	messageID := uuid.NewV7().String()
	if handoff {
		return a.completeCustomerHandoff(ctx, execution, policy, result, usage)
	}
	suppressed := false
	completed := false
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, &execution.Run, false)
		if err != nil {
			return fmt.Errorf("lock agent run for completion: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			return nil
		}
		allowed, err := policy.prepareLocked(ctx, tx, policyContext, run)
		if err != nil {
			return err
		}
		if !allowed {
			suppressed = true
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		if run.Status != string(domain.AgentRunStatusRunning) || run.InputEndSeq == nil ||
			*run.InputEndSeq != result.EndSeq || run.InputStartSeq != lane.ProcessedSeq+1 {
			return errors.New("agent run completion boundary is inconsistent")
		}
		if err := policy.persistMessage(ctx, tx, policyContext, run, messageID, domain.MessageTypeText, content); err != nil {
			return err
		}
		if mentioning, ok := policy.(mentionReplyPolicy); ok {
			if err := mentioning.applyMentions(ctx, tx, policyContext, run, messageID, content); err != nil {
				return err
			}
		}
		if deciding, ok := policy.(decisionPolicy); ok {
			if err := deciding.applyDecision(ctx, tx, policyContext, run, lane, result, messageID); err != nil {
				return err
			}
		}
		// 在最终消息事务中写入成功运行的完整过程。
		if err := syncProcess(ctx, tx, run, result.Blocks, result.Calls); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusSucceeded).
			Set("outcome = ?", result.Decision.Outcome()).
			Set("response_message_id = ?", messageID).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			Set("last_error = NULL").
			Set("error_code = NULL").
			Set("completed_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("complete agent run: %w", err)
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", result.EndSeq).
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("advance processed agent input sequence: %w", err)
		}
		if err := scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if suppressed && domain.AgentExecutionScopeKind(execution.Run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		// 记录客服门禁抑制的迟到结果。
		slog.WarnContext(ctx, "客户 Agent 迟到结果已抑制",
			"agent_run_id", execution.Run.ID,
			"conversation_id", execution.Run.ConversationID,
		)
	}
	if completed {
		logCompletedRun(ctx, execution, result.EndSeq, messageID)
	}
	return completed, nil
}

// persistPartialProcess 在运行以失败或取消结束后保留已产生的过程内容、任务清单与用量，并推进会话版本让成员重读。运行仍可继续或已成功收尾时不写入。
func (a *ExecuteAction) persistPartialProcess(ctx context.Context, initial *servermodels.AgentRun, partial agentruntime.RunResult) error {
	if len(partial.Blocks) == 0 {
		return nil
	}
	usage, err := json.Marshal(partial.Usage)
	if err != nil {
		return fmt.Errorf("encode partial agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(partial.Plan)
	if err != nil {
		return err
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockConversation(ctx, tx, initial.WorkspaceID, initial.ConversationID)
		if err != nil {
			return err
		}
		run := &servermodels.AgentRun{}
		if err := tx.NewSelect().Model(run).Where("agr.id = ?", initial.ID).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock agent run for partial process: %w", err)
		}
		// 成功运行在结果事务中已写入完整过程。
		if !agentRunStatusTerminal(run.Status) || run.Status == string(domain.AgentRunStatusSucceeded) {
			return nil
		}
		if err := syncProcess(ctx, tx, run, partial.Blocks, partial.Calls); err != nil {
			return err
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.WorkspaceID, run.ID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("persist partial agent run usage: %w", err)
		}
		return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService)
	})
}

// syncProcess 以运行交回的内容块与子 Agent 调用替换运行已保存的过程，调用首次转为待核对时通知负责人。
func syncProcess(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun, blocks []einorun.Block, calls []einorun.ToolCall) error {
	changes, err := agentprocess.SyncRecords(ctx, tx, run, blocks, calls)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(changes, func(change agentprocess.CallChange) bool { return change.NewlySettledAs(einorun.StatusNeedsReview) }) {
		return nil
	}
	return agentprocess.NotifyReviewers(ctx, tx, run.WorkspaceID, run.ID)
}

// encodeRunPlan 编码运行的任务清单，没有任务时返回 nil 使字段保持为空。
func encodeRunPlan(plan []stream.PlanTask) (*string, error) {
	if len(plan) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode agent run plan: %w", err)
	}
	text := string(encoded)
	return &text, nil
}

// logCompletedRun 记录关联客服周期的 Agent 完成结果。
func logCompletedRun(ctx context.Context, execution executionContext, endSeq int64, messageID string) {
	if domain.AgentExecutionScopeKind(execution.Run.ScopeKind) != domain.AgentExecutionScopeServiceSession {
		return
	}
	slog.InfoContext(ctx, "客户 Agent 运行完成",
		"agent_run_id", execution.Run.ID,
		"conversation_id", execution.Run.ConversationID,
		"service_session_id", execution.Run.ScopeID,
		"input_start_seq", execution.Run.InputStartSeq,
		"input_end_seq", endSeq,
		"response_message_id", messageID,
	)
}
