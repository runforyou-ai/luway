//go:build server

package conversation

import (
	"context"
	"fmt"

	deliveryaction "github.com/runforyou-ai/cervi/internal/actions/customerdelivery"
	"github.com/uptrace/bun"
)

// loadMessageDeliveries 在同一读取快照中为客户会话消息填充外部投递状态。
func loadMessageDeliveries(ctx context.Context, db bun.IDB, organizationID, conversationID string, messages []ConversationMessage) error {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	records, err := deliveryaction.ListForMessages(ctx, db, organizationID, conversationID, ids)
	if err != nil {
		return fmt.Errorf("load message deliveries: %w", err)
	}
	byMessage := make(map[string]*MessageDelivery, len(records))
	for _, record := range records {
		byMessage[record.MessageID] = &MessageDelivery{
			ID: record.ID, Status: record.Status, LastError: record.LastError, CanRetry: record.CanRetry, Paused: record.Paused,
		}
	}
	for index := range messages {
		messages[index].Delivery = byMessage[messages[index].ID]
	}
	return nil
}
