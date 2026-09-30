//go:build server

package integrationtest

import (
	"context"
	"testing"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// readWindowMessage 按定位读取返回指定消息在成员消息窗口中的当前状态。
func readWindowMessage(t *testing.T, db *bun.DB, reader *servermodels.Identity, conversationID, messageID string) conversationaction.ConversationMessage {
	t.Helper()
	page, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), reader, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, AroundMessageID: messageID})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Messages {
		if message.ID == messageID {
			return message
		}
	}
	t.Fatalf("message %s missing from window", messageID)
	return conversationaction.ConversationMessage{}
}
