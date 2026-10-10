//go:build server

// Package agentcancel 取消 Agent 执行通道中的在途运行并结算其输入队列。
package agentcancel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CancelServiceSessionRuns 取消服务周期内负责人的在途运行并结算其输入队列。
func CancelServiceSessionRuns(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID, agentIdentityID string, reason domain.AgentRunErrorCode) ([]string, error) {
	lane := &servermodels.AgentLane{}
	err := db.NewSelect().Model(lane).
		Where("al.workspace_id = ?", workspaceID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeServiceSession, serviceSessionID).
		Where("al.agent_identity_id = ?", agentIdentityID).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock assigned agent lane: %w", err)
	}
	return cancelLaneRuns(ctx, db, workspaceID, []string{lane.ID}, reason)
}

// CancelChannelRuns 推进渠道全部客户会话的版本，取消其中当前负责人的在途运行并结算输入队列，调用方已锁定渠道。
func CancelChannelRuns(ctx context.Context, db bun.IDB, workspaceID, channelID string, reason domain.AgentRunErrorCode) (int, error) {
	return cancelConversationRuns(ctx, db, workspaceID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
		Where("cc.workspace_id = ? AND ci.channel_id = ?", workspaceID, channelID), reason)
}

// CancelChannelIdentityRuns 推进渠道身份全部客户会话的版本，取消其中当前负责人的在途运行并结算输入队列，调用方已锁定渠道身份。
func CancelChannelIdentityRuns(ctx context.Context, db bun.IDB, workspaceID, channelIdentityID string, reason domain.AgentRunErrorCode) (int, error) {
	return cancelConversationRuns(ctx, db, workspaceID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Where("cc.workspace_id = ? AND cc.channel_identity_id = ?", workspaceID, channelIdentityID), reason)
}

// CancelGroupAgentRuns 取消群内指定 Agent 的在途运行并结算其输入队列。
func CancelGroupAgentRuns(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string) error {
	return cancelGroupLanes(ctx, db, workspaceID, conversationID, db.NewSelect().Model((*servermodels.AgentLane)(nil)).
		Where("al.agent_identity_id = ?", agentIdentityID))
}

// CancelGroupRuns 取消群内全部 Agent 的在途运行并结算其输入队列。
func CancelGroupRuns(ctx context.Context, db bun.IDB, workspaceID, conversationID string) error {
	return cancelGroupLanes(ctx, db, workspaceID, conversationID, db.NewSelect().Model((*servermodels.AgentLane)(nil)))
}

// cancelGroupLanes 按 Agent 身份顺序锁定群会话中选出的执行通道，取消其在途运行并结算输入队列。
func cancelGroupLanes(ctx context.Context, db bun.IDB, workspaceID, conversationID string, lanes *bun.SelectQuery) error {
	laneIDs := make([]string, 0)
	if err := lanes.Column("al.id").
		Where("al.workspace_id = ?", workspaceID).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeConversation, conversationID).
		OrderExpr("al.agent_identity_id ASC").
		For("UPDATE").
		Scan(ctx, &laneIDs); err != nil {
		return fmt.Errorf("lock group agent lanes: %w", err)
	}
	_, err := cancelLaneRuns(ctx, db, workspaceID, laneIDs, domain.AgentRunErrorCodeAgentRemoved)
	return err
}

// cancelConversationRuns 推进子查询选出的客户会话版本，取消其中当前服务周期负责人的在途运行并结算输入队列。
func cancelConversationRuns(ctx context.Context, db bun.IDB, workspaceID string, conversations *bun.SelectQuery, reason domain.AgentRunErrorCode) (int, error) {
	conversationIDs, err := chatstate.TouchConversations(ctx, db, workspaceID, conversations, domain.ConversationChangeTimeline|domain.ConversationChangeService)
	if err != nil || len(conversationIDs) == 0 {
		return 0, err
	}
	// 按编号锁定已锁定会话中当前服务周期负责人的执行通道。
	laneIDs := make([]string, 0)
	if err := db.NewSelect().TableExpr("agent_lanes AS al").
		Column("al.id").
		Join("JOIN service_sessions AS ss ON ss.workspace_id = al.workspace_id AND ss.id = al.scope_id AND ss.assignee_identity_id = al.agent_identity_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = ss.workspace_id AND svc.current_service_session_id = ss.id AND svc.conversation_id = al.conversation_id").
		Where("al.workspace_id = ? AND al.scope_kind = ? AND al.conversation_id IN (?)", workspaceID, domain.AgentExecutionScopeServiceSession, bun.List(conversationIDs)).
		OrderExpr("al.id").For("UPDATE OF al").
		Scan(ctx, &laneIDs); err != nil {
		return 0, fmt.Errorf("lock channel agent lanes: %w", err)
	}
	runIDs, err := cancelLaneRuns(ctx, db, workspaceID, laneIDs, reason)
	return len(runIDs), err
}

// cancelLaneRuns 取消已锁定执行通道中排队和执行中的运行，并把通道输入队列结算到最新序号。
func cancelLaneRuns(ctx context.Context, db bun.IDB, workspaceID string, laneIDs []string, reason domain.AgentRunErrorCode) ([]string, error) {
	runIDs := make([]string, 0)
	if len(laneIDs) == 0 {
		return runIDs, nil
	}
	if err := db.NewRaw(`
		UPDATE agent_runs
		SET status = ?, error_code = ?, completed_at = now()
		WHERE lane_id IN (?)
			AND status IN (?)
		RETURNING id
	`, domain.AgentRunStatusCancelled, reason, bun.List(laneIDs), bun.List(domain.AgentRunActiveStatuses)).
		Scan(ctx, &runIDs); err != nil {
		return nil, fmt.Errorf("cancel agent lane runs: %w", err)
	}
	if err := agentprocess.SettleEndedRuns(ctx, db, workspaceID, runIDs...); err != nil {
		return nil, err
	}
	if _, err := db.NewUpdate().Model((*servermodels.AgentLane)(nil)).
		Set("processed_seq = desired_seq").
		Where("workspace_id = ? AND id IN (?)", workspaceID, bun.List(laneIDs)).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("advance cancelled agent lanes: %w", err)
	}
	return runIDs, nil
}
