//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// assistantOwnerIdentityID 返回别名企业身份为助理时其主人的企业身份编号，其他身份返回 NULL。
func assistantOwnerIdentityID(alias string) schema.QueryWithArgs {
	return conversationaction.AssistantOwnerColumn(alias, "id")
}

// removeOwnedGroupAssistants 在调用方已锁定的群聊中移出指定主人名下仍在群内的助理并收敛其运行，返回被移出助理的快照与被取消的运行编号。
func removeOwnedGroupAssistants(ctx context.Context, tx bun.Tx, coordinator GroupAgentRunCoordinator, organizationID, conversationID, ownerUserID string) ([]conversationaction.ConversationSystemEventParticipant, []string, error) {
	rows := make([]activeGroupParticipantRow, 0)
	if err := tx.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id AS participant_id, oi.id AS identity_id, oi.display_name, cp.role").
		ColumnExpr("? AS assistant_owner_name", conversationaction.AssistantOwnerName("oi")).
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id AND oi.type = ?", domain.OrganizationIdentityTypeAssistant).
		Join("JOIN agents AS a ON a.organization_id = oi.organization_id AND a.identity_id = oi.id").
		Where("cp.organization_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", organizationID, conversationID).
		Where("a.owner_user_id = ?", ownerUserID).
		OrderExpr("oi.id ASC").
		For("UPDATE OF cp").
		Scan(ctx, &rows); err != nil {
		return nil, nil, fmt.Errorf("load owned group assistants: %w", err)
	}
	removed := make([]conversationaction.ConversationSystemEventParticipant, 0, len(rows))
	var cancelledRunIDs []string
	for _, row := range rows {
		if err := leaveGroupParticipant(ctx, tx, organizationID, row.ParticipantID); err != nil {
			return nil, nil, err
		}
		runIDs, err := coordinator.CancelForGroupAgent(ctx, tx, organizationID, conversationID, row.IdentityID)
		if err != nil {
			return nil, nil, err
		}
		cancelledRunIDs = append(cancelledRunIDs, runIDs...)
		removed = append(removed, groupParticipantSnapshot(row))
	}
	return removed, cancelledRunIDs, nil
}

// ensureAssistantsReachable 校验被点名的助理可以接收新请求，按已禁用、绑定电脑已撤销、已暂停的顺序返回冲突，与助理在线状态的优先级一致；AI 员工不受影响。
func ensureAssistantsReachable(ctx context.Context, db bun.IDB, organizationID string, identityIDs []string) error {
	var blocked struct {
		Inactive bool `bun:"inactive"`
		Unbound  bool `bun:"unbound"`
	}
	err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.status <> ? AS inactive, d.revoked_at IS NOT NULL AS unbound", domain.IdentityStatusActive).
		Join("JOIN organization_identities AS oi ON oi.organization_id = a.organization_id AND oi.id = a.identity_id AND oi.type = ?", domain.OrganizationIdentityTypeAssistant).
		Join("JOIN devices AS d ON d.organization_id = a.organization_id AND d.id = a.device_id").
		Where("a.organization_id = ? AND a.identity_id IN (?)", organizationID, bun.In(identityIDs)).
		Where("a.status <> ? OR a.paused_at IS NOT NULL OR d.revoked_at IS NOT NULL", domain.IdentityStatusActive).
		OrderExpr("a.status <> ? DESC, d.revoked_at IS NOT NULL DESC", domain.IdentityStatusActive).
		Limit(1).
		Scan(ctx, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check mentioned assistants: %w", err)
	}
	switch {
	case blocked.Inactive:
		return &conversationaction.ConflictError{Reason: ConflictReasonAssistantInactive}
	case blocked.Unbound:
		return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAssistantUnbound}
	default:
		return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAssistantPaused}
	}
}

// OwnedAssistantRetirer 在停用成员的事务中停用其名下助理并移出所有群聊。
type OwnedAssistantRetirer struct {
	coordinator GroupAgentRunCoordinator
}

// NewOwnedAssistantRetirer 创建成员名下助理的停用处理。
func NewOwnedAssistantRetirer(coordinator GroupAgentRunCoordinator) *OwnedAssistantRetirer {
	return &OwnedAssistantRetirer{coordinator: coordinator}
}

// RetireOwnedAssistants 停用指定成员名下的全部助理，并以操作人名义把它们移出所在的活跃群聊，返回被取消的运行编号。
func (r *OwnedAssistantRetirer) RetireOwnedAssistants(ctx context.Context, tx bun.Tx, actor *servermodels.Identity, ownerUserID string) ([]string, error) {
	organizationID := actor.Organization.ID
	// 锁定并更新名下全部助理，等待并发的启用提交后再置为停用；只为状态实际变化的助理推进会话版本。
	var updated []struct {
		IdentityID string `bun:"identity_id"`
		Changed    bool   `bun:"changed"`
	}
	if err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
		Set("status = ?", domain.IdentityStatusInactive).Set("updated_at = now()").
		Where("organization_id = ? AND owner_user_id = ?", organizationID, ownerUserID).
		Returning("new.identity_id, old.status <> new.status AS changed").
		Scan(ctx, &updated); err != nil {
		return nil, fmt.Errorf("deactivate owned assistants: %w", err)
	}
	for _, assistant := range updated {
		if !assistant.Changed {
			continue
		}
		if err := chatstate.TouchIdentityConversations(ctx, tx, organizationID, assistant.IdentityID); err != nil {
			return nil, err
		}
	}
	conversationIDs := make([]string, 0)
	if err := tx.NewSelect().TableExpr("conversation_participants AS cp").
		DistinctOn("cp.conversation_id").ColumnExpr("cp.conversation_id::text").
		Join("JOIN conversations AS cv ON cv.organization_id = cp.organization_id AND cv.id = cp.conversation_id AND cv.type = ? AND cv.status = ?", domain.ConversationTypeGroup, domain.ConversationStatusActive).
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN agents AS a ON a.organization_id = cs.organization_id AND a.identity_id = cs.source_id").
		Where("cp.organization_id = ? AND cp.left_at IS NULL AND a.owner_user_id = ?", organizationID, ownerUserID).
		OrderExpr("cp.conversation_id ASC").
		Scan(ctx, &conversationIDs); err != nil {
		return nil, fmt.Errorf("list owned assistant group conversations: %w", err)
	}
	var cancelledRunIDs []string
	for _, conversationID := range conversationIDs {
		conversation, err := chatstate.LockConversation(ctx, tx, organizationID, conversationID)
		if err != nil {
			return nil, err
		}
		removed, runIDs, err := removeOwnedGroupAssistants(ctx, tx, r.coordinator, organizationID, conversationID, ownerUserID)
		if err != nil {
			return nil, err
		}
		cancelledRunIDs = append(cancelledRunIDs, runIDs...)
		if len(removed) == 0 {
			continue
		}
		if _, err := appendGroupSystemEvent(ctx, tx, conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberRemoved, Actor: groupActorSnapshot(actor), Targets: removed,
		}); err != nil {
			return nil, err
		}
	}
	return cancelledRunIDs, nil
}

// CancelRunContexts 在事务提交后中断被取消运行的模型调用。
func (r *OwnedAssistantRetirer) CancelRunContexts(runIDs []string) {
	if len(runIDs) > 0 {
		r.coordinator.CancelRunContexts(runIDs)
	}
}
