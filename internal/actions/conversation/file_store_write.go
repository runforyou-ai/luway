//go:build server

// 会话共享文件区：成员与 AI 员工共用的文件列表、读取、写入、删除与从消息附件保存，只保留每个文件的当前版本。
package conversation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

func (s *Store) Write(ctx context.Context, input WriteInput) (Entry, error) {
	normalized, ok := domain.NormalizeConversationFilePath(input.Path)
	if !ok {
		return Entry{}, ErrInvalidPath
	}
	input.Path = normalized
	if input.ContentType == "" {
		input.ContentType = contentTypeFor(normalized)
	}
	pending, err := s.savePending(ctx, input)
	if err != nil {
		return Entry{}, err
	}
	entry, adopted, err := s.commit(ctx, input, pending, textfile.Hash(input.Data))
	if !adopted {
		// 未采用的新内容立即交给过期清理。
		if retireErr := retireFile(ctx, s.db, input.WorkspaceID, pending.ID); retireErr != nil {
			return Entry{}, errors.Join(err, retireErr)
		}
	}
	return entry, err
}

// Adopt 把已上传完成的内容文件按写入方式登记为文件区中的文件：按存储的内容计算摘要，内容类型取内容文件的类型，未被采用的内容交给过期清理；返回写入后的文件，内容未变化时返回原文件。
func (s *Store) Adopt(ctx context.Context, input WriteInput, fileID string) (Entry, error) {
	normalized, ok := domain.NormalizeConversationFilePath(input.Path)
	if !ok {
		return Entry{}, ErrInvalidPath
	}
	input.Path = normalized
	uploaded := &servermodels.File{}
	err := s.db.NewSelect().Model(uploaded).
		Where("f.workspace_id = ? AND f.id = ? AND f.purpose = ? AND f.status = ?", input.WorkspaceID, fileID, domain.FilePurposeConversationFile, domain.FileStatusUploaded).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrFileNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("load uploaded conversation file content: %w", err)
	}
	reader, err := s.opener.Open(ctx, uploaded)
	if err != nil {
		return Entry{}, fmt.Errorf("open uploaded conversation file content: %w", err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, reader)
	reader.Close()
	if err != nil {
		return Entry{}, fmt.Errorf("hash uploaded conversation file content: %w", err)
	}
	entry, adopted, err := s.commit(ctx, input, uploaded, hex.EncodeToString(hash.Sum(nil)))
	if !adopted {
		if retireErr := retireFile(ctx, s.db, input.WorkspaceID, uploaded.ID); retireErr != nil {
			return Entry{}, errors.Join(err, retireErr)
		}
	}
	return entry, err
}

// commit 在会话锁内把已上传的内容文件按写入方式登记为文件的当前版本、推进会话版本并通知会话受众，返回写入后的文件与内容文件是否被采用；内容未变化时返回原文件。
func (s *Store) commit(ctx context.Context, input WriteInput, pending *servermodels.File, hash string) (Entry, bool, error) {
	var id string
	err := realtime.RunInTx(ctx, s.db, func(ctx context.Context, tx bun.Tx) error {
		if input.Authorize != nil {
			if err := input.Authorize(ctx, tx); err != nil {
				return err
			}
		}
		conversation, err := chatstate.LockConversation(ctx, tx, input.WorkspaceID, input.ConversationID)
		if err != nil {
			return err
		}
		id, err = commitVersion(ctx, tx, input, pending, hash)
		if err != nil || id == "" {
			return err
		}
		return chatstate.TouchMemberConversation(ctx, tx, conversation, domain.ConversationChangeFiles)
	})
	if err != nil {
		return Entry{}, false, err
	}
	if id == "" {
		entry, _, err := s.Lookup(ctx, input.WorkspaceID, input.ConversationID, "", input.Path)
		return entry, false, err
	}
	entry, _, err := s.Lookup(ctx, input.WorkspaceID, input.ConversationID, id, "")
	return entry, true, err
}

// savePending 独立提交带过期时间的临时文件记录后写入内容并标记为已上传。
func (s *Store) savePending(ctx context.Context, input WriteInput) (*servermodels.File, error) {
	id := uuid.NewV7().String()
	record := &servermodels.File{
		ID: id, WorkspaceID: input.WorkspaceID, CreatedByUserID: input.UserID, Purpose: string(domain.FilePurposeConversationFile),
		StorageKey:   "workspaces/" + input.WorkspaceID + "/files/" + id + storageExtension(input.Path),
		OriginalName: path.Base(input.Path), ContentType: input.ContentType, ByteSize: int64(len(input.Data)), Status: string(domain.FileStatusPending),
	}
	if _, err := s.db.NewInsert().Model(record).Value("expires_at", "now() + interval '24 hours'").Exec(ctx); err != nil {
		return nil, fmt.Errorf("create conversation file content record: %w", err)
	}
	etag, err := s.writer.Save(ctx, record, input.Data)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("write conversation file content: %w", err), retireFile(ctx, s.db, input.WorkspaceID, id))
	}
	if _, err := s.db.NewUpdate().Model(record).
		Set("status = ?", domain.FileStatusUploaded).Set("etag = ?", support.NilIfZero(etag)).
		Set("uploaded_at = now()").
		Where("f.id = ? AND f.workspace_id = ?", id, input.WorkspaceID).Exec(ctx); err != nil {
		return nil, fmt.Errorf("mark conversation file content uploaded: %w", err)
	}
	return record, nil
}

// commitVersion 在会话锁内按写入方式把新内容登记为文件的当前版本并激活，被替换的版本交给过期清理；返回文件区记录编号，内容未变化时返回空编号。
func commitVersion(ctx context.Context, tx bun.Tx, input WriteInput, pending *servermodels.File, hash string) (string, error) {
	filePath := input.Path
	current := &servermodels.ConversationFile{}
	err := tx.NewSelect().Model(current).Where("cf.workspace_id = ? AND cf.conversation_id = ? AND cf.path = ?", input.WorkspaceID, input.ConversationID, filePath).Scan(ctx)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load conversation file for write: %w", err)
	}
	if input.Mode == WriteSynced {
		switch {
		case exists && current.ContentHash == hash:
			return "", nil
		case exists && current.ContentHash == input.BaseHash, !exists && input.BaseHash == "":
		default:
			filePath, err = availablePath(ctx, tx, input.WorkspaceID, input.ConversationID, conflictPath(filePath))
			if err != nil {
				return "", err
			}
			exists = false
		}
	} else if exists {
		switch input.Mode {
		case WriteNew:
			return "", ErrExists
		case WriteRenamed:
			filePath, err = availablePath(ctx, tx, input.WorkspaceID, input.ConversationID, filePath)
			if err != nil {
				return "", err
			}
			exists = false
		case WriteReplace:
			// 重复执行同一写入时文件已是目标内容，结果与首次写入相同。
			if current.ContentHash == hash {
				return "", nil
			}
			if input.BaseHash == "" {
				return "", ErrNotRead
			}
			if input.BaseHash != current.ContentHash {
				return "", ErrModified
			}
		}
	}
	activated, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
		Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").Set("original_name = ?", path.Base(filePath)).
		Where("id = ? AND workspace_id = ? AND status = ? AND expires_at > now()", pending.ID, input.WorkspaceID, domain.FileStatusUploaded).Exec(ctx)
	if err != nil {
		return "", fmt.Errorf("activate conversation file content: %w", err)
	}
	if count, err := activated.RowsAffected(); err != nil || count == 0 {
		return "", errors.Join(ErrFileNotFound, err)
	}
	if exists {
		if _, err := tx.NewUpdate().Model(current).
			Set("file_id = ?", pending.ID).Set("content_hash = ?", hash).Set("updated_by_subject_id = ?", input.SubjectID).
			WherePK().Exec(ctx); err != nil {
			return "", fmt.Errorf("update conversation file: %w", err)
		}
		return current.ID, retireFile(ctx, tx, input.WorkspaceID, current.FileID)
	}
	created := &servermodels.ConversationFile{
		ID: uuid.NewV7().String(), WorkspaceID: input.WorkspaceID, ConversationID: input.ConversationID,
		Path: filePath, FileID: pending.ID, ContentHash: hash, UpdatedBySubjectID: input.SubjectID,
	}
	if _, err := tx.NewInsert().Model(created).ExcludeColumn("created_at", "updated_at").Exec(ctx); err != nil {
		return "", fmt.Errorf("create conversation file: %w", err)
	}
	return created.ID, nil
}

// conflictPath 返回冲突副本的路径：在文件名与扩展名之间追加冲突标记。
func conflictPath(filePath string) string {
	extension := path.Ext(path.Base(filePath))
	return strings.TrimSuffix(filePath, extension) + conflictSuffix + extension
}

// availablePath 返回 filePath 未被使用时的 filePath 本身，否则返回在文件名与扩展名之间依次追加 (2)、(3)……后尚未使用的路径。
func availablePath(ctx context.Context, tx bun.Tx, workspaceID, conversationID, filePath string) (string, error) {
	extension := path.Ext(path.Base(filePath))
	stem := strings.TrimSuffix(filePath, extension)
	for index := 1; ; index++ {
		candidate := filePath
		if index > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, index, extension)
		}
		taken, err := tx.NewSelect().Model((*servermodels.ConversationFile)(nil)).
			Where("workspace_id = ? AND conversation_id = ? AND path = ?", workspaceID, conversationID, candidate).Exists(ctx)
		if err != nil {
			return "", fmt.Errorf("check conversation file path: %w", err)
		}
		if !taken {
			return candidate, nil
		}
	}
}
