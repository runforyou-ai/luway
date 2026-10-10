//go:build server

package groupchat

import (
	"context"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// mentionTargetRow 是校验群提醒目标时读取的参与者行。
type mentionTargetRow struct {
	ChatSubjectID string  `bun:"chat_subject_id"`
	Kind          string  `bun:"kind"`
	SourceID      string  `bun:"source_id"`
	DisplayName   *string `bun:"display_name"`
	IdentityType  string  `bun:"identity_type"`
}

// loadGroupMentionTargets 校验提醒目标是当前群聊中的有效参与者。
func loadGroupMentionTargets(ctx context.Context, db bun.IDB, workspaceID, conversationID, senderSubjectID string, subjectIDs []string) ([]conversationaction.ConversationMessageMention, error) {
	if len(subjectIDs) == 0 {
		return []conversationaction.ConversationMessageMention{}, nil
	}
	rows := make([]mentionTargetRow, 0, len(subjectIDs))
	if err := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cs.id AS chat_subject_id").
		ColumnExpr("cs.kind AS kind").
		ColumnExpr("cs.source_id AS source_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.type AS identity_type").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = cs.workspace_id AND oi.id = cs.source_id").
		Where("cp.workspace_id = ?", workspaceID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		Where("cp.subject_id IN (?)", bun.List(subjectIDs)).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load group mention targets: %w", err)
	}
	if len(rows) != len(subjectIDs) {
		return nil, &conversationaction.ConflictError{Reason: ConflictReasonGroupMentionTargetInvalid}
	}
	// 按发送时的提醒顺序返回，供被点名 AI 员工的发言先后使用。
	targets := make(map[string]mentionTargetRow, len(rows))
	for _, row := range rows {
		if row.ChatSubjectID == senderSubjectID {
			return nil, &conversationaction.ConflictError{Reason: ConflictReasonGroupMentionTargetInvalid}
		}
		targets[row.ChatSubjectID] = row
	}
	mentions := make([]conversationaction.ConversationMessageMention, 0, len(rows))
	for _, subjectID := range subjectIDs {
		row := targets[subjectID]
		mentions = append(mentions, conversationaction.ConversationMessageMention{
			ChatSubjectID: row.ChatSubjectID, Kind: domain.ChatSubjectKind(row.Kind),
			SourceID: row.SourceID, DisplayName: row.DisplayName,
			IdentityType: domain.WorkspaceIdentityType(row.IdentityType),
		})
	}
	return mentions, nil
}
