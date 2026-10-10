//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureParticipant 取得或创建聊天主体在会话中的成员参与者，新建时使用 participantID；已退出的参与者以成员角色重新加入。
func EnsureParticipant(ctx context.Context, db bun.IDB, workspaceID, conversationID, subjectID, participantID string) (*servermodels.ConversationParticipant, error) {
	participant, err := findParticipant(ctx, db, workspaceID, conversationID, subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		participant = &servermodels.ConversationParticipant{
			ID: participantID, WorkspaceID: workspaceID, ConversationID: conversationID,
			SubjectID: subjectID, Role: string(domain.ConversationParticipantRoleMember),
		}
		err = db.NewInsert().Model(participant).
			Column("id", "workspace_id", "conversation_id", "subject_id", "role").
			On("CONFLICT (workspace_id, conversation_id, subject_id) DO NOTHING").
			Returning("*").
			Scan(ctx)
		if err == nil {
			return participant, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("create conversation participant: %w", err)
		}
		// 未插入时由新查询读取并发创建者已提交的参与者。
		participant, err = findParticipant(ctx, db, workspaceID, conversationID, subjectID)
	}
	if err != nil {
		return nil, fmt.Errorf("find conversation participant: %w", err)
	}
	if participant.LeftAt == nil {
		return participant, nil
	}
	if _, err := db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("left_at = NULL").
		Set("role = ?", domain.ConversationParticipantRoleMember).
		Where("workspace_id = ? AND id = ?", workspaceID, participant.ID).
		Where("left_at IS NOT NULL").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("restore conversation participant: %w", err)
	}
	participant.LeftAt = nil
	participant.Role = string(domain.ConversationParticipantRoleMember)
	return participant, nil
}

// findParticipant 读取聊天主体在会话中的参与者。
func findParticipant(ctx context.Context, db bun.IDB, workspaceID, conversationID, subjectID string) (*servermodels.ConversationParticipant, error) {
	participant := &servermodels.ConversationParticipant{}
	err := db.NewSelect().Model(participant).
		Where("cp.workspace_id = ? AND cp.conversation_id = ? AND cp.subject_id = ?", workspaceID, conversationID, subjectID).
		Scan(ctx)
	return participant, err
}
