//go:build server

// 会话共享文件区：成员与 AI 员工共用的文件列表、读取、写入、删除与从消息附件保存，只保留每个文件的当前版本。
package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrFileNotFound 表示会话共享文件区中没有该文件，或附件不属于该会话；勿与 ErrConversationNotFound 混淆。
	ErrFileNotFound = errors.New("conversation file not found")
	// ErrInvalidPath 表示路径越出文件区、为空或不合法。
	ErrInvalidPath = errors.New("conversation file path is invalid")
	// ErrExists 表示只新建的写入遇到同名文件。
	ErrExists = errors.New("conversation file already exists")
	// ErrNotRead 表示覆盖已有文件前没有读取记录。
	ErrNotRead = errors.New("conversation file must be read before overwrite")
	// ErrModified 表示文件在读取后已被改动。
	ErrModified = errors.New("conversation file modified after read")
)

// Store 读写会话共享文件区：内容写入文件存储，文件区记录指向当前版本，被替换或删除的版本交给过期清理。
type Store struct {
	db     *bun.DB
	writer filecontent.Writer
	opener filecontent.Opener
}

// NewStore 创建会话文件区读写。
func NewStore(db *bun.DB, writer filecontent.Writer, opener filecontent.Opener) *Store {
	return &Store{db: db, writer: writer, opener: opener}
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

// Lookup 按编号或路径返回文件区中的一个文件，没有时返回 ErrFileNotFound。
func (s *Store) Lookup(ctx context.Context, workspaceID, conversationID, id, filePath string) (Entry, *servermodels.File, error) {
	var entry Entry
	query := entryQuery(s.db, workspaceID, conversationID)
	if id != "" {
		if !str.IsUUID(id) {
			return Entry{}, nil, ErrFileNotFound
		}
		query = query.Where("cf.id = ?", id)
	} else {
		query = query.Where("cf.path = ?", filePath)
	}
	err := query.Scan(ctx, &entry)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, nil, ErrFileNotFound
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
