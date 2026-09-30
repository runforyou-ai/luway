//go:build server

package agentrun

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// scheduleNextRun 在活动运行让出后为执行范围安排下一次运行。
func scheduleNextRun(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, policy agentRunPolicy, policyContext agentRunPolicyContext,
	organizationID string, scopeKind domain.AgentExecutionScopeKind, scopeID string) error {
	lanes, err := loadPendingLanes(ctx, db, organizationID, scopeKind, scopeID)
	if err != nil {
		return err
	}
	if len(lanes) == 0 {
		return nil
	}
	// 待处理队列已锁定后再判断互斥，执行范围内已有活动运行时由该运行的终态继续轮转。
	active, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.organization_id = ?", organizationID).
		Where("agr.scope_kind = ? AND agr.scope_id = ?", scopeKind, scopeID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check active agent run for rotation: %w", err)
	}
	if active {
		return nil
	}
	for _, lane := range lanes {
		revisionID, eligible, err := policy.laneRevision(ctx, db, policyContext, &lane)
		if err != nil {
			return err
		}
		if !eligible {
			slog.Info("轮转跳过失去资格的 AI 员工",
				"organization_id", lane.OrganizationID, "conversation_id", lane.ConversationID,
				"agent_identity_id", lane.AgentIdentityID, "lane_id", lane.ID)
			continue
		}
		_, err = insertAndDispatchRun(ctx, db, enqueuer, agentRunSpec{
			OrganizationID: lane.OrganizationID, ConversationID: lane.ConversationID,
			AgentIdentityID: lane.AgentIdentityID, RevisionID: revisionID,
			ScopeKind: domain.AgentExecutionScopeKind(lane.ScopeKind), ScopeID: lane.ScopeID,
		}, lane.ID, lane.ProcessedSeq+1)
		if err != nil {
			return err
		}
		slog.Info("执行范围轮转到下一位 AI 员工",
			"organization_id", lane.OrganizationID, "conversation_id", lane.ConversationID,
			"agent_identity_id", lane.AgentIdentityID, "lane_id", lane.ID)
		return nil
	}
	return nil
}

// loadPendingLanes 按最早一条未处理输入的消息顺序读取本执行范围内待安排的输入队列。
func loadPendingLanes(ctx context.Context, db bun.IDB, organizationID string, scopeKind domain.AgentExecutionScopeKind, scopeID string) ([]servermodels.AgentLane, error) {
	lanes := make([]servermodels.AgentLane, 0)
	// 队列内输入序号连续，最早一条未处理输入即 processed_seq + 1。
	if err := db.NewSelect().Model(&lanes).
		ColumnExpr("al.*").
		Join("JOIN agent_inputs AS ai ON ai.lane_id = al.id AND ai.input_seq = al.processed_seq + 1").
		Join("JOIN messages AS msg ON msg.id = ai.source_message_id AND msg.organization_id = ai.organization_id").
		Where("al.organization_id = ?", organizationID).
		Where("al.scope_kind = ? AND al.scope_id = ?", scopeKind, scopeID).
		Where("al.desired_seq > al.processed_seq").
		OrderExpr("msg.message_seq ASC, ai.source_ordinal ASC, al.id ASC").
		For("UPDATE OF al").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load pending agent lanes: %w", err)
	}
	return lanes, nil
}
