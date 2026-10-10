//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// StopAgentReply 按成员归属停止独立 AI 会话中的运行。
func (c *RunCancellation) StopAgentReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string) (domain.AgentRunStatus, error) {
	return c.stopReply(ctx, identity, conversationID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
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
			Join("JOIN agent_conversations AS ac ON ac.workspace_id = agr.workspace_id AND ac.conversation_id = agr.conversation_id AND ac.agent_identity_id = agr.agent_identity_id").
			Where("agr.workspace_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Workspace.ID, conversationID, runID).
			Where("ac.user_identity_id = ? AND agr.scope_kind = ?", identity.WorkspaceIdentity.ID, domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return agentChatRunPolicy{enqueuer: c.enqueuer}, run, nil
	})
}

// StopGroupAgentReply 由群内有效成员停止本群在途的 AI 员工运行。
func (c *RunCancellation) StopGroupAgentReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string) (domain.AgentRunStatus, error) {
	return c.stopReply(ctx, identity, conversationID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
		if _, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable); err != nil {
			return nil, nil, err
		}
		run := &servermodels.AgentRun{}
		err := tx.NewSelect().Model(run).
			Where("agr.workspace_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Workspace.ID, conversationID, runID).
			Where("agr.scope_kind = ?", domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return groupMentionRunPolicy{scheduler: NewScheduler(c.enqueuer)}, run, nil
	})
}

// StopServiceCopilotReply 由可阅读 Copilot 线程的成员停止线程中的运行。
func (c *RunCancellation) StopServiceCopilotReply(ctx context.Context, identity *servermodels.Identity, threadID, runID string) (domain.AgentRunStatus, error) {
	return c.stopReply(ctx, identity, threadID, runID, func(ctx context.Context, tx bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error) {
		// 停止按副驾驶线程的阅读资格授权，AI 员工停用后仍可停止线程中的运行。
		if err := conversationaccess.RequireReadable(ctx, tx, identity, threadID, conversationaccess.KindCopilot); err != nil {
			return nil, nil, err
		}
		run := &servermodels.AgentRun{}
		err := tx.NewSelect().Model(run).
			Join("JOIN service_copilot_threads AS sct ON sct.workspace_id = agr.workspace_id AND sct.conversation_id = agr.conversation_id AND sct.agent_identity_id = agr.agent_identity_id").
			Where("agr.workspace_id = ? AND agr.conversation_id = ? AND agr.id = ?", identity.Workspace.ID, threadID, runID).
			Where("agr.scope_kind = ?", domain.AgentExecutionScopeConversation).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, chatstate.ErrConversationNotFound
		}
		if err != nil {
			return nil, nil, err
		}
		return copilotRunPolicy{enqueuer: c.enqueuer}, run, nil
	})
}

// stopReply 在调用方给出的访问守卫内结束一次运行，并安排执行范围内的下一次运行。
func (c *RunCancellation) stopReply(ctx context.Context, identity *servermodels.Identity, conversationID, runID string,
	authorize func(context.Context, bun.Tx) (agentRunPolicy, *servermodels.AgentRun, error)) (domain.AgentRunStatus, error) {
	var status domain.AgentRunStatus
	stopped := false
	err := realtime.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		policy, initial, err := authorize(ctx, tx)
		if err != nil {
			return err
		}
		locked, err := lockAgentRun(ctx, tx, policy, initial, false)
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
			Set("completed_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.WorkspaceID, run.ID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", lane.DesiredSeq).WherePK().Exec(ctx); err != nil {
			return err
		}
		if err := scheduleNextRun(ctx, tx, c.enqueuer, policy, locked.PolicyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID); err != nil {
			return err
		}
		status, stopped = domain.AgentRunStatusCancelled, true
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("stop agent reply: %w", err)
	}
	if stopped {
		slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "成员停止 AI 回复", "conversation_id", conversationID, "agent_run_id", runID, "user_id", identity.User.ID)
	}
	return status, nil
}
