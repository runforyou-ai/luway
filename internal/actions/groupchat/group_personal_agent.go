//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// removeGroupPersonalAgents 在调用方已锁定的群聊中移出指定负责人仍在群内的个人 AI 员工，收敛其运行并取消其在群内等待确认或审批的操作，返回被移出 AI 员工的快照。
func removeGroupPersonalAgents(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, coordinator GroupAgentRunCoordinator, workspaceID, conversationID, responsibleUserID string) ([]conversationaction.ConversationSystemEventParticipant, error) {
	rows := make([]activeGroupParticipantRow, 0)
	if err := tx.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id AS participant_id, oi.id AS identity_id, oi.display_name, cp.role").
		ColumnExpr("? AS personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = cs.workspace_id AND oi.id = cs.source_id").
		Join("JOIN agents AS a ON a.workspace_id = oi.workspace_id AND a.identity_id = oi.id").
		Where("cp.workspace_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", workspaceID, conversationID).
		Where("? = ANY(a.service_audiences) AND a.responsible_user_id = ?", domain.ServiceAudiencePersonal, responsibleUserID).
		OrderExpr("oi.id ASC").
		For("UPDATE OF cp").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load group personal agents: %w", err)
	}
	removed := make([]conversationaction.ConversationSystemEventParticipant, 0, len(rows))
	for _, row := range rows {
		if err := leaveGroupParticipant(ctx, tx, workspaceID, row.ParticipantID); err != nil {
			return nil, err
		}
		if err := coordinator.CancelForGroupAgent(ctx, tx, workspaceID, conversationID, row.IdentityID); err != nil {
			return nil, err
		}
		if err := agentprocess.CancelConversationParticipantToolDecisions(ctx, tx, enqueuer, workspaceID, conversationID, row.IdentityID); err != nil {
			return nil, err
		}
		removed = append(removed, groupParticipantSnapshot(row))
	}
	return removed, nil
}

// ensurePersonalAgentsReachable 校验被点名的个人 AI 员工可以接收新请求，按已禁用、电脑已撤销、已暂停的顺序返回冲突；其他 AI 员工不受影响。
func ensurePersonalAgentsReachable(ctx context.Context, db bun.IDB, workspaceID string, identityIDs []string) error {
	var blocked struct {
		Inactive bool `bun:"inactive"`
		Unbound  bool `bun:"unbound"`
	}
	err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.status <> ? AS inactive, cmp.revoked_at IS NOT NULL AS unbound", domain.IdentityStatusActive).
		Join("JOIN computers AS cmp ON cmp.workspace_id = a.workspace_id AND cmp.id = a.computer_id").
		Where("a.workspace_id = ? AND a.identity_id IN (?)", workspaceID, bun.List(identityIDs)).
		Where("? = ANY(a.service_audiences)", domain.ServiceAudiencePersonal).
		Where("a.status <> ? OR a.paused_at IS NOT NULL OR cmp.revoked_at IS NOT NULL", domain.IdentityStatusActive).
		OrderExpr("a.status <> ? DESC, cmp.revoked_at IS NOT NULL DESC", domain.IdentityStatusActive).
		Limit(1).
		Scan(ctx, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check mentioned personal agents: %w", err)
	}
	switch {
	case blocked.Inactive:
		return &conversationaction.ConflictError{Reason: ConflictReasonPersonalAgentInactive}
	case blocked.Unbound:
		return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonPersonalAgentUnbound}
	default:
		return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonPersonalAgentPaused}
	}
}

// PersonalAgentRetirer 在停用成员的事务中停用其负责的个人 AI 员工并移出所有群聊。
type PersonalAgentRetirer struct {
	enqueuer    servertask.TxEnqueuer
	coordinator GroupAgentRunCoordinator
}

// NewPersonalAgentRetirer 创建成员负责的个人 AI 员工的停用处理。
func NewPersonalAgentRetirer(enqueuer servertask.TxEnqueuer, coordinator GroupAgentRunCoordinator) *PersonalAgentRetirer {
	return &PersonalAgentRetirer{enqueuer: enqueuer, coordinator: coordinator}
}

// RetirePersonalAgents 停用指定成员负责的全部个人 AI 员工，取消它们等待确认或审批的操作，并以操作人名义把它们移出所在的活跃群聊。
func (r *PersonalAgentRetirer) RetirePersonalAgents(ctx context.Context, tx bun.Tx, actor *servermodels.Identity, responsibleUserID string) error {
	workspaceID := actor.Workspace.ID
	// 锁定并更新其负责的全部个人 AI 员工，等待并发的启用提交后再置为停用；只为状态实际变化的 AI 员工推进会话版本。
	var updated []struct {
		IdentityID string `bun:"identity_id"`
		Changed    bool   `bun:"changed"`
	}
	if err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
		Set("status = ?", domain.IdentityStatusInactive).
		Where("workspace_id = ? AND responsible_user_id = ? AND ? = ANY(service_audiences)", workspaceID, responsibleUserID, domain.ServiceAudiencePersonal).
		Returning("new.identity_id, old.status <> new.status AS changed").
		Scan(ctx, &updated); err != nil {
		return fmt.Errorf("deactivate personal agents: %w", err)
	}
	for _, agent := range updated {
		if err := agentprocess.CancelAgentToolDecisions(ctx, tx, r.enqueuer, workspaceID, agent.IdentityID); err != nil {
			return err
		}
		if !agent.Changed {
			continue
		}
		if err := chatstate.TouchIdentityConversations(ctx, tx, workspaceID, agent.IdentityID); err != nil {
			return err
		}
	}
	conversationIDs := make([]string, 0)
	if err := tx.NewSelect().TableExpr("conversation_participants AS cp").
		DistinctOn("cp.conversation_id").ColumnExpr("cp.conversation_id::text").
		Join("JOIN conversations AS cv ON cv.workspace_id = cp.workspace_id AND cv.id = cp.conversation_id AND cv.type = ? AND cv.status = ?", domain.ConversationTypeGroup, domain.ConversationStatusActive).
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN agents AS a ON a.workspace_id = cs.workspace_id AND a.identity_id = cs.source_id").
		Where("cp.workspace_id = ? AND cp.left_at IS NULL AND a.responsible_user_id = ? AND ? = ANY(a.service_audiences)", workspaceID, responsibleUserID, domain.ServiceAudiencePersonal).
		OrderExpr("cp.conversation_id ASC").
		Scan(ctx, &conversationIDs); err != nil {
		return fmt.Errorf("list personal agent group conversations: %w", err)
	}
	for _, conversationID := range conversationIDs {
		conversation, err := chatstate.LockConversation(ctx, tx, workspaceID, conversationID)
		if err != nil {
			return err
		}
		removed, err := removeGroupPersonalAgents(ctx, tx, r.enqueuer, r.coordinator, workspaceID, conversationID, responsibleUserID)
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			continue
		}
		if _, err := appendGroupSystemEvent(ctx, tx, conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberRemoved, Actor: groupActorSnapshot(actor), Targets: removed,
		}); err != nil {
			return err
		}
	}
	return nil
}
