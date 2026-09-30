//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// LoadMessageAttachments 批量读取当前消息窗口中的附件元数据。
func LoadMessageAttachments(ctx context.Context, db bun.IDB, organizationID string, messages []ConversationMessage) error {
	ids := make([]string, 0)
	for _, message := range messages {
		if message.Type == domain.MessageTypeAttachment {
			ids = append(ids, message.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows := []struct {
		MessageID string `bun:"message_id"`
		MessageAttachment
	}{}
	if err := db.NewSelect().TableExpr("message_attachments AS ma").
		ColumnExpr("ma.message_id, COALESCE(ma.file_id::text, '') AS id, ma.name, ma.content_type, ma.byte_size, ma.image_width, ma.image_height, ma.transfer_status").
		Where("ma.organization_id = ? AND ma.message_id IN (?)", organizationID, bun.In(ids)).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("load message attachments: %w", err)
	}
	byMessage := make(map[string]MessageAttachment, len(rows))
	for _, row := range rows {
		byMessage[row.MessageID] = row.MessageAttachment
	}
	for index := range messages {
		if messages[index].Type != domain.MessageTypeAttachment {
			continue
		}
		attachment, found := byMessage[messages[index].ID]
		if !found {
			return ErrDataInvariant
		}
		messages[index].Attachment = &attachment
	}
	return nil
}

// SaveCustomerAttachment 写入客户会话消息的附件关联。
func SaveCustomerAttachment(ctx context.Context, tx bun.IDB, organizationID, messageID string, attachment MessageAttachment) error {
	if _, err := tx.NewRaw(`INSERT INTO message_attachments
 (message_id, organization_id, file_id, name, content_type, byte_size, image_width, image_height, transfer_status)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		messageID, organizationID, common.OptionalString(attachment.ID), attachment.Name, attachment.ContentType, attachment.ByteSize,
		attachment.ImageWidth, attachment.ImageHeight, attachment.TransferStatus).Exec(ctx); err != nil {
		return fmt.Errorf("save customer message attachment: %w", err)
	}
	return nil
}

// GetAttachmentFile 读取当前成员可见消息所关联的有效文件。
func (q *ListConversationMessagesQuery) GetAttachmentFile(ctx context.Context, identity *servermodels.Identity, conversationID, messageID string) (*servermodels.File, error) {
	if !common.ValidUUID(conversationID) || !common.ValidUUID(messageID) {
		return nil, ErrConversationNotFound
	}
	if err := AuthorizeConversationHistory(ctx, q.db, identity, conversationID); err != nil {
		return nil, err
	}
	record := &servermodels.File{}
	err := q.db.NewSelect().Model(record).
		Join("JOIN message_attachments AS ma ON ma.organization_id = f.organization_id AND ma.file_id = f.id").
		Join("JOIN messages AS msg ON msg.organization_id = ma.organization_id AND msg.id = ma.message_id").
		Where("msg.organization_id = ? AND msg.conversation_id = ? AND msg.id = ? AND msg.deleted_at IS NULL", identity.Organization.ID, conversationID, messageID).
		Where("f.status = ?", domain.FileStatusActive).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	return record, err
}
