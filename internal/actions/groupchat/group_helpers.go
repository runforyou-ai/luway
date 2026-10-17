//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// activeGroupParticipantRow 是群聊当前参与者及其个人 AI 员工负责人。
type activeGroupParticipantRow struct {
	ParticipantID             string  `bun:"participant_id"`
	IdentityID                string  `bun:"identity_id"`
	DisplayName               string  `bun:"display_name"`
	Role                      string  `bun:"role"`
	PersonalResponsibleUserID string  `bun:"personal_responsible_user_id"`
	PersonalResponsibleName   *string `bun:"personal_responsible_name"`
}

// loadActiveGroupParticipantIdentityIDs 读取群聊当前有效成员编号。
func loadActiveGroupParticipantIdentityIDs(ctx context.Context, db bun.IDB, workspaceID, conversationID string) ([]string, error) {
	identityIDs := make([]string, 0)
	if err := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cs.source_id").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Where("cp.workspace_id = ?", workspaceID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		OrderExpr("cs.source_id ASC").
		Scan(ctx, &identityIDs); err != nil {
		return nil, fmt.Errorf("load active group participant identity ids: %w", err)
	}
	return identityIDs, nil
}

// loadActiveGroupParticipant 锁定指定的当前有效群成员。
func loadActiveGroupParticipant(ctx context.Context, db bun.IDB, workspaceID, conversationID, identityID string, requireActiveUser bool) (activeGroupParticipantRow, error) {
	row := activeGroupParticipantRow{}
	query := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id AS participant_id").
		ColumnExpr("cs.source_id AS identity_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("cp.role AS role").
		ColumnExpr("CASE WHEN ? = ANY(a.service_audiences) THEN a.responsible_user_id::text ELSE '' END AS personal_responsible_user_id", domain.ServiceAudiencePersonal).
		ColumnExpr("? AS personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ? AND cs.source_id = ?", domain.ChatSubjectKindWorkspaceIdentity, identityID).
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = cs.workspace_id AND oi.id = cs.source_id").
		Join("LEFT JOIN agents AS a ON a.workspace_id = oi.workspace_id AND a.identity_id = oi.id")
	if requireActiveUser {
		query = query.Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id AND u.status = ?", domain.IdentityStatusActive)
	}
	err := query.
		Where("cp.workspace_id = ?", workspaceID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		For("UPDATE OF cp").
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return activeGroupParticipantRow{}, &conversationaction.ConflictError{Reason: ConflictReasonGroupMemberNotActive}
	}
	if err != nil {
		return activeGroupParticipantRow{}, fmt.Errorf("load active group participant: %w", err)
	}
	return row, nil
}

// leaveGroupParticipant 标记成员已经退出群聊。
func leaveGroupParticipant(ctx context.Context, db bun.IDB, workspaceID, participantID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("left_at = now()").
		Set("role = ?", domain.ConversationParticipantRoleMember).
		Where("workspace_id = ?", workspaceID).
		Where("id = ?", participantID).
		Where("left_at IS NULL").
		Exec(ctx); err != nil {
		return fmt.Errorf("leave group conversation participant: %w", err)
	}
	return nil
}

// createGroupSystemEvent 写入类型化系统事件并推进会话摘要与操作人的已读位置。
func createGroupSystemEvent(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversation *servermodels.Conversation, event conversationaction.ConversationSystemEvent) (*servermodels.Message, error) {
	message, err := appendGroupSystemEvent(ctx, db, conversation, event)
	if err != nil {
		return nil, err
	}
	state := &servermodels.ConversationUserState{
		WorkspaceID: identity.Workspace.ID, ConversationID: conversation.ID,
		UserID: identity.User.ID, LastReadMessageID: &message.ID,
	}
	if err := conversationaction.AdvanceConversationUserReadState(ctx, db, state, message); err != nil {
		return nil, err
	}
	return message, nil
}

// appendGroupSystemEvent 写入类型化系统事件并推进会话摘要。
func appendGroupSystemEvent(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, event conversationaction.ConversationSystemEvent) (*servermodels.Message, error) {
	if event.Targets == nil {
		event.Targets = make([]conversationaction.ConversationSystemEventParticipant, 0)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal group system event: %w", err)
	}
	eventType := string(event.Type)
	message := &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: conversation.WorkspaceID, ConversationID: conversation.ID,
		Type: string(domain.MessageTypeSystem), Body: "", SystemEventType: &eventType, SystemEventPayload: payload,
	}
	message, _, err = chatstate.AppendSystemEvent(ctx, db, conversation, message)
	return message, err
}

// groupActorSnapshot 记录操作人的审计快照。
func groupActorSnapshot(identity *servermodels.Identity) conversationaction.ConversationSystemEventParticipant {
	return conversationaction.ConversationSystemEventParticipant{IdentityID: identity.WorkspaceIdentity.ID, DisplayName: identity.WorkspaceIdentity.DisplayName}
}

// groupParticipantSnapshot 记录目标成员的审计快照。
func groupParticipantSnapshot(participant activeGroupParticipantRow) conversationaction.ConversationSystemEventParticipant {
	return conversationaction.ConversationSystemEventParticipant{IdentityID: participant.IdentityID, DisplayName: participant.DisplayName, PersonalResponsibleName: participant.PersonalResponsibleName}
}
