//go:build server

package groupchat

import (
	"context"
	"fmt"
	"slices"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
func loadGroupMentionTargets(ctx context.Context, db bun.IDB, organizationID, conversationID, senderSubjectID string, subjectIDs []string) ([]conversationaction.ConversationMessageMention, error) {
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
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id").
		Where("cp.organization_id = ?", organizationID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		Where("cp.subject_id IN (?)", bun.In(subjectIDs)).
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
			IdentityType: domain.OrganizationIdentityType(row.IdentityType),
		})
	}
	return mentions, nil
}

// loadIdempotentGroupMessage 校验群消息的完整发送意图。
func loadIdempotentGroupMessage(ctx context.Context, db bun.IDB, identity *servermodels.Identity, input GroupTextMessageInput, idempotencyKey string) (conversationaction.ConversationMessage, bool, error) {
	saved, found, err := conversationaction.LoadIdempotentMemberMessage(ctx, db, identity, conversationaction.InternalTextExpectation(input.ConversationID, input.Body, input.ReplyToMessageID), idempotencyKey)
	if err != nil || !found {
		return saved, found, err
	}
	var stored struct {
		MentionAll bool `bun:"mention_all"`
	}
	if err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.mention_all AS mention_all").
		Where("msg.organization_id = ?", identity.Organization.ID).
		Where("msg.id = ?", saved.ID).
		Scan(ctx, &stored); err != nil {
		return conversationaction.ConversationMessage{}, true, fmt.Errorf("load idempotent group mention all: %w", err)
	}
	storedMentionSubjectIDs := make([]string, 0)
	if err := db.NewSelect().
		TableExpr("message_mentions AS mm").
		ColumnExpr("mm.subject_id").
		Where("mm.organization_id = ?", identity.Organization.ID).
		Where("mm.message_id = ?", saved.ID).
		OrderExpr("mm.subject_id ASC").
		Scan(ctx, &storedMentionSubjectIDs); err != nil {
		return conversationaction.ConversationMessage{}, true, fmt.Errorf("load idempotent group mentions: %w", err)
	}
	sentMentionSubjectIDs := append([]string(nil), input.MentionSubjectIDs...)
	slices.Sort(sentMentionSubjectIDs)
	if stored.MentionAll != input.MentionAll || !slices.Equal(storedMentionSubjectIDs, sentMentionSubjectIDs) {
		return conversationaction.ConversationMessage{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	saved.Mentions, err = conversationaction.LoadPersistedMessageMentions(ctx, db, identity.Organization.ID, saved.ID)
	if err != nil {
		return conversationaction.ConversationMessage{}, true, err
	}
	saved.MentionAll = stored.MentionAll
	return saved, true, nil
}
