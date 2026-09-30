//go:build server

package conversation

import (
	"context"
	"fmt"

	translationaction "github.com/runforyou-ai/cervi/internal/actions/translation"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadMessageTranslations 为已识别语言的消息填充当前成员语言的已保存译文。
func loadMessageTranslations(ctx context.Context, db bun.IDB, identity *servermodels.Identity, messages []ConversationMessage) error {
	ids := make([]string, 0)
	for _, message := range messages {
		if message.Language != nil {
			ids = append(ids, message.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	language := translationaction.ViewerLanguage(identity)
	var rows []servermodels.MessageTranslation
	if err := db.NewSelect().Model(&rows).
		Column("message_id", "body").
		Where("organization_id = ? AND language = ? AND message_id IN (?)", identity.Organization.ID, language, bun.In(ids)).
		Scan(ctx); err != nil {
		return fmt.Errorf("load message translations: %w", err)
	}
	bodies := make(map[string]string, len(rows))
	for _, row := range rows {
		bodies[row.MessageID] = row.Body
	}
	for index := range messages {
		if body, ok := bodies[messages[index].ID]; ok {
			messages[index].Translation = &MessageTranslation{Language: language, Body: body}
		}
	}
	return nil
}
