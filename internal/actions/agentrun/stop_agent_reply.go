//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// StopAgentReply 按成员归属停止独立 AI 会话中的运行，并在提交后尽力中断模型调用。
func (a *ExecuteAction) StopAgentReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string) (domain.AgentRunStatus, error) {
	return a.stopReply(ctx, identity, conversationID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
		member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
		if err != nil {
			return nil, nil, err
		}
		if member.Conversation.Type != string(domain.ConversationTypeAgent) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		// 读取权限与新消息发送资格分离，禁用 Agent 后仍可停止自己的运行。
		run := &servermodels.AgentRun{}
		err = tx.NewSelect().Model(run).
			Join("JOIN agent_conversations AS ac ON ac.organization_id = agr.organization_id AND ac.conversation_id = agr.conversation_id AND ac.agent_identity_id = agr.agent_identity_id").
			Where("agr.organization_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Organization.ID, conversationID, runID).
			Where("ac.user_identity_id = ? AND agr.scope_kind = ?", identity.OrganizationIdentity.ID, domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return agentChatRunPolicy{enqueuer: a.enqueuer}, run, nil
	})
}

// StopGroupAgentReply 由群内有效成员停止本群在途的 AI 员工运行。
func (a *ExecuteAction) StopGroupAgentReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string) (domain.AgentRunStatus, error) {
	return a.stopReply(ctx, identity, conversationID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
		if _, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable); err != nil {
			return nil, nil, err
		}
		run := &servermodels.AgentRun{}
		err := tx.NewSelect().Model(run).
			Where("agr.organization_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Organization.ID, conversationID, runID).
			Where("agr.scope_kind = ?", domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return groupMentionRunPolicy{scheduler: NewScheduler(a.enqueuer)}, run, nil
	})
}

// StopServiceCopilotReply 由可读取所属服务会话的成员停止 Copilot 线程中的运行。
func (a *ExecuteAction) StopServiceCopilotReply(ctx context.Context, identity *servermodels.Identity, threadID, runID string) (domain.AgentRunStatus, error) {
	return a.stopReply(ctx, identity, threadID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
		// 读取权限与新提问资格分离，AI 员工停用后仍可停止线程中的运行。
		run := &servermodels.AgentRun{}
		err := tx.NewSelect().Model(run).
			Join("JOIN service_copilot_threads AS sct ON sct.organization_id = agr.organization_id AND sct.conversation_id = agr.conversation_id AND sct.agent_identity_id = agr.agent_identity_id").
			Join("JOIN service_conversations AS svc ON svc.organization_id = sct.organization_id AND svc.conversation_id = sct.served_conversation_id").
			Where("agr.organization_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Organization.ID, threadID, runID).
			Where("agr.scope_kind = ?", domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return copilotRunPolicy{}, run, nil
	})
}

// stopReply 在调用方给出的访问守卫内结束一次运行，并安排执行范围内的下一次运行。
func (a *ExecuteAction) stopReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string,
	authorize func(context.Context, bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error)) (domain.AgentRunStatus, error) {
	if !common.ValidUUID(conversationID) || !common.ValidUUID(runID) {
		return "", chatstate.ErrConversationNotFound
	}
	var status domain.AgentRunStatus
	stopped := false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		policy, initial, err := authorize(ctx, tx)
		if err != nil {
			return err
		}
		locked, err := lockAgentRun(ctx, tx, policy, initial)
		if err != nil {
			return err
		}
		run, lane := locked.Run, locked.Lane
		status = domain.AgentRunStatus(run.Status)
		if agentRunStatusTerminal(run.Status) {
			return nil
		}
		// 停止边界包含已提交但尚未被模型认领的输入，后到消息另起运行。
		if run.InputStartSeq != lane.ProcessedSeq+1 || lane.DesiredSeq < run.InputStartSeq {
			return errors.New("stopped agent run boundary is inconsistent")
		}
		seqs, err := claimLaneInputs(ctx, tx, run, lane.ProcessedSeq, lane.DesiredSeq)
		if err != nil {
			return err
		}
		if int64(len(seqs)) != lane.DesiredSeq-lane.ProcessedSeq {
			return errors.New("stopped agent input sequence is not contiguous")
		}
		messageID := uuid.NewV7().String()
		if err := policy.persistMessage(ctx, tx, locked.PolicyContext, run, messageID, domain.MessageTypeAgentCancelled, ""); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusCancelled).
			Set("error_code = ?", domain.AgentRunErrorCodeUserCancelled).
			Set("response_message_id = ?", messageID).
			Set("input_end_seq = ?", lane.DesiredSeq).
			Set("completed_at = now()").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		// 设备执行的运行推进设备工作水位，设备据此立即续租并得知停止。
		if run.ExecutionDeviceID != nil {
			if err := advanceDeviceWork(ctx, tx, run.OrganizationID, *run.ExecutionDeviceID); err != nil {
				return err
			}
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", lane.DesiredSeq).
			Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		if err := scheduleNextRun(ctx, tx, a.enqueuer, policy, locked.PolicyContext, run.OrganizationID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID); err != nil {
			return err
		}
		status, stopped = domain.AgentRunStatusCancelled, true
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("stop agent reply: %w", err)
	}
	if status == domain.AgentRunStatusCancelled {
		a.CancelRunContexts([]string{runID})
	}
	if stopped {
		slog.Info("成员停止 AI 回复", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "agent_run_id", runID, "user_id", identity.User.ID)
	}
	return status, nil
}
