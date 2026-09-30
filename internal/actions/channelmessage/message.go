//go:build server

// Package channelmessage 维护渠道平台消息身份与本地引用关系。
package channelmessage

import (
	"context"
	"fmt"

	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Inbound 定义渠道入站消息的稳定平台事实。
type Inbound struct {
	AccountID      string
	ConversationID string
	MessageID      string
	Reply          *Reply
}

// Reply 保存同一聊天内被引用消息的编号和一层快照。
type Reply struct {
	MessageID   string
	Body        string
	SenderName  string
	SenderIsBot bool
}

// Record 在渠道身份锁保护的事务中保存映射，并关联先到达的引用消息。
func Record(ctx context.Context, db bun.IDB, record *models.ChannelMessage) error {
	if _, err := db.NewInsert().Model(record).Exec(ctx); err != nil {
		return err
	}
	// 当前消息和其原消息任意一方后到达时，都按完整平台身份关联。
	_, err := db.ExecContext(ctx, `UPDATE messages AS msg SET reply_to_message_id = target.message_id
        FROM channel_messages AS source
        JOIN channel_messages AS target ON target.organization_id = source.organization_id
            AND target.conversation_id = source.conversation_id AND target.channel_id = source.channel_id
            AND target.provider_account_id = source.provider_account_id AND target.provider_conversation_id = source.provider_conversation_id
            AND target.provider_message_id = source.reply_provider_message_id
        WHERE msg.id = source.message_id AND msg.organization_id = source.organization_id
            AND msg.conversation_id = source.conversation_id AND msg.reply_to_message_id IS NULL
            AND source.organization_id = ? AND source.channel_id = ? AND source.provider_account_id = ? AND source.provider_conversation_id = ?
            AND (source.message_id = ? OR target.message_id = ?)`,
		record.OrganizationID, record.ChannelID, record.ProviderAccountID, record.ProviderConversationID, record.MessageID, record.MessageID)
	return err
}

// RecordInbound 保存新接收的渠道消息的平台身份和引用快照。
func RecordInbound(ctx context.Context, db bun.IDB, channelID string, message *models.Message, input *Inbound) error {
	record := &models.ChannelMessage{
		MessageID: message.ID, OrganizationID: message.OrganizationID, ConversationID: message.ConversationID,
		ChannelID: channelID, ProviderAccountID: input.AccountID, ProviderConversationID: input.ConversationID, ProviderMessageID: input.MessageID,
	}
	if input.Reply != nil {
		record.ReplyProviderMessageID = &input.Reply.MessageID
		record.ReplyBody, record.ReplySenderName, record.ReplySenderIsBot = input.Reply.Body, input.Reply.SenderName, input.Reply.SenderIsBot
	}
	return Record(ctx, db, record)
}

// MatchesInbound 按稳定的平台身份核对入站消息重放。
func MatchesInbound(ctx context.Context, db bun.IDB, message *models.Message, channelID string, input *Inbound) (bool, error) {
	var record models.ChannelMessage
	if err := db.NewSelect().Model(&record).Where("cm.message_id = ? AND cm.organization_id = ?", message.ID, message.OrganizationID).Scan(ctx); err != nil {
		return false, fmt.Errorf("load channel message mapping: %w", err)
	}
	var storedReply, incomingReply string
	if record.ReplyProviderMessageID != nil {
		storedReply = *record.ReplyProviderMessageID
	}
	if input.Reply != nil {
		incomingReply = input.Reply.MessageID
	}
	return record.ChannelID == channelID && record.ProviderAccountID == input.AccountID && record.ProviderConversationID == input.ConversationID && record.ProviderMessageID == input.MessageID && storedReply == incomingReply, nil
}
