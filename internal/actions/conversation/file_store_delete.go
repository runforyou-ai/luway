//go:build server

// 会话共享文件区：成员与 AI 员工共用的文件列表、读取、写入、删除与从消息附件保存，只保留每个文件的当前版本。
package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

func (s *Store) Delete(ctx context.Context, workspaceID, conversationID, id string, authorize func(context.Context, bun.Tx) error) error {
	if !str.IsUUID(id) {
		return ErrFileNotFound
	}
	return realtime.RunInTx(ctx, s.db, func(ctx context.Context, tx bun.Tx) error {
		if authorize != nil {
			if err := authorize(ctx, tx); err != nil {
				return err
			}
		}
		conversation, err := chatstate.LockConversation(ctx, tx, workspaceID, conversationID)
		if err != nil {
			return err
		}
		var fileID string
		err = tx.NewDelete().Model((*servermodels.ConversationFile)(nil)).
			Where("workspace_id = ? AND conversation_id = ? AND id = ?", workspaceID, conversationID, id).
			Returning("file_id").Scan(ctx, &fileID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFileNotFound
		}
		if err != nil {
			return fmt.Errorf("delete conversation file: %w", err)
		}
		if err := retireFile(ctx, tx, workspaceID, fileID); err != nil {
			return err
		}
		return chatstate.TouchMemberConversation(ctx, tx, conversation, domain.ConversationChangeFiles)
	})
}

// SaveAttachment 把 sourceConversationID 会话中一条附件消息的文件复制为文件区中的文件，路径为空时使用附件名称。
func (s *Store) SaveAttachment(ctx context.Context, input WriteInput, sourceConversationID, messageID string) (Entry, error) {
	if !str.IsUUID(messageID) {
		return Entry{}, ErrFileNotFound
	}
	var attachment struct {
		FileID string `bun:"file_id"`
		Name   string `bun:"name"`
	}
	err := s.db.NewSelect().TableExpr("message_attachments AS ma").ColumnExpr("ma.file_id, ma.name").
		Join("JOIN messages AS msg ON msg.workspace_id = ma.workspace_id AND msg.id = ma.message_id").
		Join("JOIN files AS f ON f.workspace_id = ma.workspace_id AND f.id = ma.file_id").
		Where("msg.workspace_id = ? AND msg.conversation_id = ? AND msg.id = ?", input.WorkspaceID, sourceConversationID, messageID).
		Where("msg.deleted_at IS NULL AND f.status = ?", domain.FileStatusActive).
		Scan(ctx, &attachment)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrFileNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("load attachment for conversation file: %w", err)
	}
	file := &servermodels.File{}
	if err := s.db.NewSelect().Model(file).Where("f.workspace_id = ? AND f.id = ?", input.WorkspaceID, attachment.FileID).Scan(ctx); err != nil {
		return Entry{}, fmt.Errorf("load attachment file for conversation file: %w", err)
	}
	if input.Data, err = s.open(ctx, file); err != nil {
		return Entry{}, err
	}
	if input.Path == "" {
		input.Path = attachment.Name
	}
	input.ContentType = file.ContentType
	return s.Write(ctx, input)
}

// retireFile 把不再使用的内容文件交给过期清理任务删除。
func retireFile(ctx context.Context, db bun.IDB, workspaceID, fileID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.File)(nil)).
		Set("status = ?", domain.FileStatusDeleting).Set("expires_at = now()").
		Where("id = ? AND workspace_id = ?", fileID, workspaceID).Exec(ctx); err != nil {
		return fmt.Errorf("retire conversation file content: %w", err)
	}
	return nil
}

// contentTypeFor 按路径扩展名推断内容类型：先按文档格式，再按系统登记的类型，都无法推断时按纯文本处理。
func contentTypeFor(filePath string) string {
	if contentType := domain.KnowledgeDocumentContentType(filePath); contentType != "" {
		return contentType
	}
	if mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension(path.Ext(filePath))); err == nil {
		return mediaType
	}
	return "text/plain"
}

// storageExtension 返回存储键使用的扩展名：取路径中由小写字母或数字组成的扩展名，否则为 .bin。
func storageExtension(filePath string) string {
	extension := strings.ToLower(path.Ext(filePath))
	if len(extension) < 2 || len(extension) > 17 || strings.ContainsFunc(extension[1:], func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		return ".bin"
	}
	return extension
}
