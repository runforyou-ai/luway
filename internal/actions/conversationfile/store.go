//go:build server

// Package conversationfile 管理会话共享文件区：成员与 AI 员工共用的文件列表、读取、写入、删除与从消息附件保存，只保留每个文件的当前版本。
package conversationfile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrNotFound 表示文件区中没有该文件，或附件不属于该会话。
	ErrNotFound = errors.New("conversation file not found")
	// ErrInvalidPath 表示路径越出文件区、为空或不合法。
	ErrInvalidPath = errors.New("conversation file path is invalid")
	// ErrExists 表示只新建的写入遇到同名文件。
	ErrExists = errors.New("conversation file already exists")
	// ErrNotRead 表示覆盖已有文件前没有读取记录。
	ErrNotRead = errors.New("conversation file must be read before overwrite")
	// ErrModified 表示文件在读取后已被改动。
	ErrModified = errors.New("conversation file modified after read")
)

// ContentWriter 按文件记录中确定的存储类型写入内容。
type ContentWriter interface {
	Save(context.Context, *servermodels.File, []byte) (string, error)
}

// ContentOpener 按文件记录流式读取内容。
type ContentOpener interface {
	Open(context.Context, *servermodels.File) (io.ReadCloser, error)
}

// Store 读写会话共享文件区：内容写入文件存储，文件区记录指向当前版本，被替换或删除的版本交给过期清理。
type Store struct {
	db      *bun.DB
	backend func() domain.FileStorageBackend
	writer  ContentWriter
	opener  ContentOpener
}

// NewStore 创建会话文件区读写，backend 返回新内容写入的存储类型。
func NewStore(db *bun.DB, backend func() domain.FileStorageBackend, writer ContentWriter, opener ContentOpener) *Store {
	return &Store{db: db, backend: backend, writer: writer, opener: opener}
}

// Entry 是会话共享文件区中的一个文件及其当前版本。
type Entry struct {
	ID                 string    `bun:"id"`
	Path               string    `bun:"path"`
	FileID             string    `bun:"file_id"`
	ContentType        string    `bun:"content_type"`
	ByteSize           int64     `bun:"byte_size"`
	ContentHash        string    `bun:"content_hash"`
	UpdatedAt          time.Time `bun:"updated_at"`
	UpdatedBySubjectID string    `bun:"updated_by_subject_id"`
	UpdatedByName      *string   `bun:"updated_by_name"`
}

// Content 是读取到的文件当前版本内容。
type Content struct {
	Entry
	Data []byte
}

// WriteMode 定义写入遇到同名文件时的处理方式。
type WriteMode int

const (
	// WriteReplace 覆盖已有文件：内容已一致时直接返回，否则要求 BaseHash 与当前摘要一致；文件不存在时新建。
	WriteReplace WriteMode = iota
	// WriteNew 只新建，同名文件已存在时返回 ErrExists。
	WriteNew
	// WriteRenamed 只新建，同名文件已存在时在文件名后追加序号。
	WriteRenamed
	// WriteSynced 按同步基准写入：文件当前摘要等于 BaseHash（BaseHash 为空表示文件不存在）时写入，内容已一致时直接返回，否则文件在同步后已被改动或删除，内容另存为冲突副本。
	WriteSynced
)

// conflictSuffix 是电脑同步时冲突副本文件名中追加的标记。
const conflictSuffix = "（电脑上的版本）"

// WriteInput 是一次写入：文件区所属会话、路径、内容、写入方式与修改人。
type WriteInput struct {
	WorkspaceID    string
	ConversationID string
	Path           string
	Data           []byte
	// ContentType 为空时按路径扩展名推断。
	ContentType string
	Mode        WriteMode
	BaseHash    string
	// SubjectID 是修改人的聊天主体编号，UserID 是写入文件记录的上传用户编号。
	SubjectID string
	UserID    string
	// Authorize 非空时在写入事务开始时锁定并复核修改人仍可使用文件区，返回错误时放弃写入。
	Authorize func(context.Context, bun.Tx) error
}

// AreaConversation 返回会话所用文件区的会话编号：Copilot 线程使用所服务会话的文件区，其他会话使用自己的文件区。
func AreaConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (string, error) {
	var served string
	err := db.NewSelect().Model((*servermodels.ServiceCopilotThread)(nil)).Column("served_conversation_id").
		Where("workspace_id = ? AND conversation_id = ?", workspaceID, conversationID).Scan(ctx, &served)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationID, nil
	}
	if err != nil {
		return "", fmt.Errorf("load copilot served conversation for files: %w", err)
	}
	return served, nil
}

// entryQuery 选出文件区中的文件及当前版本的类型、大小与修改人名称。
func entryQuery(db bun.IDB, workspaceID, conversationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("conversation_files AS cf").
		ColumnExpr("cf.id, cf.path, cf.file_id, f.content_type, f.byte_size, cf.content_hash, cf.updated_at, cf.updated_by_subject_id").
		ColumnExpr(agentprocess.SubjectName("cf.updated_by_subject_id")+" AS updated_by_name").
		Join("JOIN files AS f ON f.workspace_id = cf.workspace_id AND f.id = cf.file_id").
		Where("cf.workspace_id = ? AND cf.conversation_id = ?", workspaceID, conversationID)
}

// List 返回文件区中的全部文件，按路径排序。
func (s *Store) List(ctx context.Context, workspaceID, conversationID string) ([]Entry, error) {
	entries := []Entry{}
	if err := entryQuery(s.db, workspaceID, conversationID).OrderExpr("cf.path").Scan(ctx, &entries); err != nil {
		return nil, fmt.Errorf("list conversation files: %w", err)
	}
	return entries, nil
}

// Lookup 按编号或路径返回文件区中的一个文件，没有时返回 ErrNotFound。
func (s *Store) Lookup(ctx context.Context, workspaceID, conversationID, id, filePath string) (Entry, *servermodels.File, error) {
	var entry Entry
	query := entryQuery(s.db, workspaceID, conversationID)
	if id != "" {
		if !str.IsUUID(id) {
			return Entry{}, nil, ErrNotFound
		}
		query = query.Where("cf.id = ?", id)
	} else {
		query = query.Where("cf.path = ?", filePath)
	}
	err := query.Scan(ctx, &entry)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, nil, ErrNotFound
	}
	if err != nil {
		return Entry{}, nil, fmt.Errorf("load conversation file: %w", err)
	}
	file := &servermodels.File{}
	if err := s.db.NewSelect().Model(file).Where("f.workspace_id = ? AND f.id = ?", workspaceID, entry.FileID).Scan(ctx); err != nil {
		return Entry{}, nil, fmt.Errorf("load conversation file content record: %w", err)
	}
	return entry, file, nil
}

// Read 读取文件区中指定路径文件的当前内容，超过 limit 字节时只返回文件信息与空内容。
func (s *Store) Read(ctx context.Context, workspaceID, conversationID, filePath string, limit int64) (Content, error) {
	normalized, ok := domain.NormalizeConversationFilePath(filePath)
	if !ok {
		return Content{}, ErrInvalidPath
	}
	entry, file, err := s.Lookup(ctx, workspaceID, conversationID, "", normalized)
	if err != nil {
		return Content{}, err
	}
	if entry.ByteSize > limit {
		return Content{Entry: entry}, nil
	}
	data, err := s.open(ctx, file)
	if err != nil {
		return Content{}, err
	}
	return Content{Entry: entry, Data: data}, nil
}

// open 读取文件记录的全部内容。
func (s *Store) open(ctx context.Context, file *servermodels.File) ([]byte, error) {
	reader, err := s.opener.Open(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("open conversation file content: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read conversation file content: %w", err)
	}
	return data, nil
}

// Write 把内容写为文件区中的文件：先写入新的存储文件，再在会话锁内按写入方式更新文件区记录、推进会话版本并通知会话受众，被替换的版本交给过期清理；返回写入后的文件，内容未变化时返回原文件。
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
		return Entry{}, ErrNotFound
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
		StorageBackend: string(s.backend()), StorageKey: "workspaces/" + input.WorkspaceID + "/files/" + id + storageExtension(input.Path),
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
		return "", errors.Join(ErrNotFound, err)
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

// Delete 删除文件区中的文件、推进会话版本并通知会话受众，内容交给过期清理；authorize 非空时在事务开始时锁定并复核删除人仍可使用文件区。
func (s *Store) Delete(ctx context.Context, workspaceID, conversationID, id string, authorize func(context.Context, bun.Tx) error) error {
	if !str.IsUUID(id) {
		return ErrNotFound
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
			return ErrNotFound
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
		return Entry{}, ErrNotFound
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
		return Entry{}, ErrNotFound
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
