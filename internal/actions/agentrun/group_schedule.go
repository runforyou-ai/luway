//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ScheduleGroupMentions 在群消息事务内按点名顺序为被点名的 Agent 追加输入。
func (s *Scheduler) ScheduleGroupMentions(ctx context.Context, db bun.IDB, organizationID, conversationID, messageID, senderSubjectID string, agentIdentityIDs []string) error {
	for ordinal, agentIdentityID := range agentIdentityIDs {
		revisionID, eligible, err := loadGroupAgentRevision(ctx, db, organizationID, conversationID, agentIdentityID, true)
		if err != nil {
			return err
		}
		if !eligible {
			slog.Warn("群内被点名的 AI 员工不满足执行资格",
				"organization_id", organizationID,
				"conversation_id", conversationID,
				"agent_identity_id", agentIdentityID,
				"message_id", messageID,
			)
			continue
		}
		if err := s.appendInput(ctx, db, agentRunSpec{
			OrganizationID: organizationID, ConversationID: conversationID,
			AgentIdentityID: agentIdentityID, RevisionID: revisionID,
			ScopeKind: domain.AgentExecutionScopeConversation, ScopeID: conversationID,
			Kind: domain.AgentInputKindMention, SourceSubjectID: senderSubjectID, SourceOrdinal: ordinal,
		}, messageID); err != nil {
			return fmt.Errorf("schedule group agent mention: %w", err)
		}
	}
	return nil
}

// CancelForGroupAgent 在成员变化事务内取消群内指定 Agent 的在途运行、结算其输入队列并轮转到下一位。
func (a *ExecuteAction) CancelForGroupAgent(ctx context.Context, db bun.IDB, organizationID, conversationID, agentIdentityID string) ([]string, error) {
	runIDs, err := cancelGroupAgentRuns(ctx, db, organizationID, conversationID, agentIdentityID)
	if err != nil {
		return nil, err
	}
	if err := a.rotateGroupScope(ctx, db, organizationID, conversationID); err != nil {
		return nil, err
	}
	return runIDs, nil
}

// rotateGroupScope 在群会话事务内为让出的活动运行名额安排下一位 AI 员工。
func (a *ExecuteAction) rotateGroupScope(ctx context.Context, db bun.IDB, organizationID, conversationID string) error {
	conversation, err := chatstate.LockConversation(ctx, db, organizationID, conversationID)
	if err != nil {
		return err
	}
	policy := groupMentionRunPolicy{scheduler: NewScheduler(a.enqueuer)}
	return scheduleNextRun(ctx, db, a.enqueuer, policy, agentRunPolicyContext{Conversation: conversation},
		organizationID, domain.AgentExecutionScopeConversation, conversationID)
}

// CancelForGroupConversation 在群解散事务内取消群内全部 Agent 的在途运行。
func (a *ExecuteAction) CancelForGroupConversation(ctx context.Context, db bun.IDB, organizationID, conversationID string) ([]string, error) {
	agentIdentityIDs := make([]string, 0)
	if err := db.NewSelect().Model((*servermodels.AgentLane)(nil)).
		ColumnExpr("al.agent_identity_id").
		Where("al.organization_id = ?", organizationID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeConversation, conversationID).
		OrderExpr("al.agent_identity_id ASC").
		Scan(ctx, &agentIdentityIDs); err != nil {
		return nil, fmt.Errorf("load group agent lanes: %w", err)
	}
	runIDs := make([]string, 0)
	for _, agentIdentityID := range agentIdentityIDs {
		cancelled, err := cancelGroupAgentRuns(ctx, db, organizationID, conversationID, agentIdentityID)
		if err != nil {
			return nil, err
		}
		runIDs = append(runIDs, cancelled...)
	}
	return runIDs, nil
}

// cancelGroupAgentRuns 取消一条群内 Agent 队列上的在途运行并结算其输入。
func cancelGroupAgentRuns(ctx context.Context, db bun.IDB, organizationID, conversationID, agentIdentityID string) ([]string, error) {
	lane := &servermodels.AgentLane{}
	err := db.NewSelect().Model(lane).
		Where("al.organization_id = ?", organizationID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeConversation, conversationID).
		Where("al.agent_identity_id = ?", agentIdentityID).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock group agent lane: %w", err)
	}
	runIDs := make([]string, 0)
	if err := db.NewRaw(`
		UPDATE agent_runs
		SET status = ?, error_code = ?, completed_at = now(), updated_at = now()
		WHERE lane_id = ?
			AND status IN (?, ?)
		RETURNING id
	`, domain.AgentRunStatusCancelled, domain.AgentRunErrorCodeAgentRemoved, lane.ID,
		domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(ctx, &runIDs); err != nil {
		return nil, fmt.Errorf("cancel group agent runs: %w", err)
	}
	if _, err := db.NewUpdate().Model(lane).
		Set("processed_seq = desired_seq").
		Set("updated_at = now()").
		WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("advance cancelled group agent lane: %w", err)
	}
	return runIDs, nil
}
