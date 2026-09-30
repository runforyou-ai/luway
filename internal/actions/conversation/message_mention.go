//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type groupMentionTargetRow struct {
	MessageID     string  `bun:"message_id"`
	ChatSubjectID string  `bun:"chat_subject_id"`
	Kind          string  `bun:"kind"`
	SourceID      string  `bun:"source_id"`
	DisplayName   *string `bun:"display_name"`
	IdentityType  string  `bun:"identity_type"`
}

// loadConversationMessageMentions 批量补充一页消息的提醒主体。
func loadConversationMessageMentions(ctx context.Context, db bun.IDB, organizationID string, messages []ConversationMessage) error {
	if len(messages) == 0 {
		return nil
	}
	messageIDs := make([]string, 0, len(messages))
	messageIndexes := make(map[string]int, len(messages))
	for index := range messages {
		messages[index].Mentions = []ConversationMessageMention{}
		messageIDs = append(messageIDs, messages[index].ID)
		messageIndexes[messages[index].ID] = index
	}
	rows := make([]groupMentionTargetRow, 0)
	if err := db.NewSelect().
		TableExpr("message_mentions AS mm").
		ColumnExpr("mm.message_id AS message_id").
		ColumnExpr("cs.id AS chat_subject_id").
		ColumnExpr("cs.kind AS kind").
		ColumnExpr("cs.source_id AS source_id").
		ColumnExpr("oi.display_name AS display_name").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = mm.organization_id AND cs.id = mm.subject_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Where("mm.organization_id = ?", organizationID).
		Where("mm.message_id IN (?)", bun.In(messageIDs)).
		OrderExpr("mm.message_id ASC, mm.subject_id ASC").
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("load conversation message mentions: %w", err)
	}
	for _, row := range rows {
		index, exists := messageIndexes[row.MessageID]
		if !exists {
			return ErrDataInvariant
		}
		messages[index].Mentions = append(messages[index].Mentions, ConversationMessageMention{
			ChatSubjectID: row.ChatSubjectID, Kind: domain.ChatSubjectKind(row.Kind),
			SourceID: row.SourceID, DisplayName: row.DisplayName,
		})
	}
	return nil
}

// CreateMessageMentions 持久化消息提醒关系。
func CreateMessageMentions(ctx context.Context, db bun.IDB, organizationID, messageID string, mentions []ConversationMessageMention) error {
	if len(mentions) == 0 {
		return nil
	}
	rows := make([]*servermodels.MessageMention, 0, len(mentions))
	for _, mention := range mentions {
		rows = append(rows, &servermodels.MessageMention{
			OrganizationID: organizationID, MessageID: messageID, SubjectID: mention.ChatSubjectID,
		})
	}
	if _, err := db.NewInsert().Model(&rows).
		Column("organization_id", "message_id", "subject_id").
		Exec(ctx); err != nil {
		return fmt.Errorf("create message mentions: %w", err)
	}
	return nil
}

// LoadPersistedMessageMentions 读取一条消息已经保存的提醒主体。
func LoadPersistedMessageMentions(ctx context.Context, db bun.IDB, organizationID, messageID string) ([]ConversationMessageMention, error) {
	messages := []ConversationMessage{{ID: messageID}}
	if err := loadConversationMessageMentions(ctx, db, organizationID, messages); err != nil {
		return nil, err
	}
	return messages[0].Mentions, nil
}
