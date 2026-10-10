//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// LoadMessageAttachments 批量读取当前消息窗口中的附件元数据。
func LoadMessageAttachments(ctx context.Context, db bun.IDB, workspaceID string, messages []ConversationMessage) error {
	ids := arr.FilterMap(messages, func(message ConversationMessage) (string, bool) {
		return message.ID, message.Type == domain.MessageTypeAttachment
	})
	if len(ids) == 0 {
		return nil
	}
	rows := []struct {
		MessageID string `bun:"message_id"`
		MessageAttachment
	}{}
	if err := db.NewSelect().TableExpr("message_attachments AS ma").
		ColumnExpr("ma.message_id, COALESCE(ma.file_id::text, '') AS id, ma.name, ma.content_type, ma.byte_size, ma.image_width, ma.image_height, ma.transfer_status").
		Where("ma.workspace_id = ? AND ma.message_id IN (?)", workspaceID, bun.List(ids)).Scan(ctx, &rows); err != nil {
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

// SaveMessageAttachment 写入消息的附件关联。
func SaveMessageAttachment(ctx context.Context, tx bun.IDB, workspaceID, messageID string, attachment MessageAttachment) error {
	if _, err := tx.NewRaw(`INSERT INTO message_attachments
 (message_id, workspace_id, file_id, name, content_type, byte_size, image_width, image_height, transfer_status)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		messageID, workspaceID, support.NilIfZero(attachment.ID), attachment.Name, attachment.ContentType, attachment.ByteSize,
		attachment.ImageWidth, attachment.ImageHeight, attachment.TransferStatus).Exec(ctx); err != nil {
		return fmt.Errorf("save message attachment: %w", err)
	}
	return nil
}

// GetAttachmentFile 读取当前成员可见消息所关联的有效文件。
func (q *ListConversationMessagesQuery) GetAttachmentFile(ctx context.Context, identity *servermodels.Identity, conversationID, messageID string) (*servermodels.File, error) {
	if err := conversationaccess.RequireReadable(ctx, q.db, identity, conversationID); err != nil {
		return nil, err
	}
	record := &servermodels.File{}
	err := q.db.NewSelect().Model(record).
		Join("JOIN message_attachments AS ma ON ma.workspace_id = f.workspace_id AND ma.file_id = f.id").
		Join("JOIN messages AS msg ON msg.workspace_id = ma.workspace_id AND msg.id = ma.message_id").
		Where("msg.workspace_id = ? AND msg.conversation_id = ? AND msg.id = ? AND msg.deleted_at IS NULL", identity.Workspace.ID, conversationID, messageID).
		Where("f.status = ?", domain.FileStatusActive).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	return record, err
}
