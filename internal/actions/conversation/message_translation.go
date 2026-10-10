//go:build server

package conversation

import (
	"context"
	"fmt"

	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// loadMessageTranslations 为已识别语言的消息填充当前成员语言的已保存译文。
func loadMessageTranslations(ctx context.Context, db bun.IDB, identity *servermodels.Identity, messages []ConversationMessage) error {
	ids := arr.FilterMap(messages, func(message ConversationMessage) (string, bool) { return message.ID, message.Language != nil })
	if len(ids) == 0 {
		return nil
	}
	language := translationaction.ViewerLanguage(identity)
	var rows []servermodels.MessageTranslation
	if err := db.NewSelect().Model(&rows).
		Column("message_id", "body").
		Where("workspace_id = ? AND language = ? AND message_id IN (?)", identity.Workspace.ID, language, bun.List(ids)).
		Scan(ctx); err != nil {
		return fmt.Errorf("load message translations: %w", err)
	}
	bodies := arr.Associate(rows, func(row servermodels.MessageTranslation) (string, string) { return row.MessageID, row.Body })
	for index := range messages {
		if body, ok := bodies[messages[index].ID]; ok {
			messages[index].Translation = &MessageTranslation{Language: language, Body: body}
		}
	}
	return nil
}
