//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CancelServiceSessionRuns 取消服务周期内负责人的在途运行并结算其输入队列。
func CancelServiceSessionRuns(ctx context.Context, db bun.IDB, organizationID, serviceSessionID, agentIdentityID string, reason domain.AgentRunErrorCode) ([]string, error) {
	lane := &servermodels.AgentLane{}
	err := db.NewSelect().Model(lane).
		Where("al.organization_id = ?", organizationID).
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
	return cancelLaneRuns(ctx, db, organizationID, []string{lane.ID}, reason)
}

// CancelChannelRuns 推进渠道全部客户会话的版本，取消其中当前负责人的在途运行并结算输入队列，调用方已锁定渠道。
func CancelChannelRuns(ctx context.Context, db bun.IDB, organizationID, channelID string, reason domain.AgentRunErrorCode) (int, error) {
	conversationIDs, err := TouchConversations(ctx, db, organizationID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Where("cc.organization_id = ? AND cci.channel_id = ?", organizationID, channelID), domain.ConversationChangeTimeline|domain.ConversationChangeService)
	if err != nil || len(conversationIDs) == 0 {
		return 0, err
	}
	// 按编号锁定已锁定会话中当前服务周期负责人的执行通道。
	laneIDs := make([]string, 0)
	if err := db.NewSelect().TableExpr("agent_lanes AS al").
		Column("al.id").
		Join("JOIN service_sessions AS ss ON ss.organization_id = al.organization_id AND ss.id = al.scope_id AND ss.assignee_identity_id = al.agent_identity_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = ss.organization_id AND svc.current_service_session_id = ss.id AND svc.conversation_id = al.conversation_id").
		Where("al.organization_id = ? AND al.scope_kind = ? AND al.conversation_id IN (?)", organizationID, domain.AgentExecutionScopeServiceSession, bun.In(conversationIDs)).
		OrderExpr("al.id").For("UPDATE OF al").
		Scan(ctx, &laneIDs); err != nil {
		return 0, fmt.Errorf("lock channel agent lanes: %w", err)
	}
	runIDs, err := cancelLaneRuns(ctx, db, organizationID, laneIDs, reason)
	return len(runIDs), err
}

// cancelLaneRuns 取消已锁定执行通道中排队和执行中的运行，并把通道输入队列结算到最新序号。
func cancelLaneRuns(ctx context.Context, db bun.IDB, organizationID string, laneIDs []string, reason domain.AgentRunErrorCode) ([]string, error) {
	runIDs := make([]string, 0)
	if len(laneIDs) == 0 {
		return runIDs, nil
	}
	if err := db.NewRaw(`
		UPDATE agent_runs
		SET status = ?, error_code = ?, completed_at = now(), updated_at = now()
		WHERE lane_id IN (?)
			AND status IN (?, ?)
		RETURNING id
	`, domain.AgentRunStatusCancelled, reason, bun.In(laneIDs), domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(ctx, &runIDs); err != nil {
		return nil, fmt.Errorf("cancel agent lane runs: %w", err)
	}
	if _, err := db.NewUpdate().Model((*servermodels.AgentLane)(nil)).
		Set("processed_seq = desired_seq").
		Set("updated_at = now()").
		Where("organization_id = ? AND id IN (?)", organizationID, bun.In(laneIDs)).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("advance cancelled agent lanes: %w", err)
	}
	return runIDs, nil
}
