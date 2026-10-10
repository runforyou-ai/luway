//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"

	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrSyncUnavailable 表示调用不是这台电脑上执行中的命令或本机 Agent 轮次，不能同步会话共享文件区。
var ErrSyncUnavailable = errors.New("computer operation cannot sync shared files")

// SharedFile 是会话共享文件区中的一个文件及其当前内容文件。
type SharedFile struct {
	conversationfile.Entry
	Content *servermodels.File
}

// SharedUploadInput 定义电脑为写回一个文件申请的内容上传：文件区内路径、内容类型与字节数。
type SharedUploadInput struct {
	Path        string
	ContentType string
	ByteSize    int64
}

// SharedCommitInput 定义电脑写回一个文件：文件区内路径、已上传的内容文件与电脑上次同步时该文件的摘要，摘要为空表示上次同步时文件不存在。
type SharedCommitInput struct {
	Path     string
	FileID   string
	BaseHash string
}

// SharedFilesAction 为电脑上执行中的命令与本机 Agent 轮次同步所属会话的共享文件区：给出文件清单、创建内容上传并把上传完成的内容写回。
type SharedFilesAction struct {
	db    *bun.DB
	store *conversationfile.Store
}

// NewSharedFilesAction 创建电脑同步会话共享文件区的处理。
func NewSharedFilesAction(db *bun.DB, store *conversationfile.Store) *SharedFilesAction {
	return &SharedFilesAction{db: db, store: store}
}

// author 返回调用所属运行写入共享文件区的文件区与署名；调用须派发到这台电脑、正在执行且是命令或本机 Agent 轮次。
func (a *SharedFilesAction) author(ctx context.Context, computer Identity, callID string) (conversationfile.RunAuthor, error) {
	if !str.IsUUID(callID) {
		return conversationfile.RunAuthor{}, ErrSyncUnavailable
	}
	call := &servermodels.AgentToolCall{}
	err := a.db.NewSelect().Model(call).Column("agent_run_id", "operation").
		Where("atc.workspace_id = ? AND atc.id = ? AND atc.computer_id = ? AND atc.status = ?", computer.WorkspaceID, callID, computer.ComputerID, domain.AgentToolCallRunning).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (call.Operation == nil || !call.Operation.Operation.Kind.SyncsSharedFiles()) {
		return conversationfile.RunAuthor{}, ErrSyncUnavailable
	}
	if err != nil {
		return conversationfile.RunAuthor{}, fmt.Errorf("load syncing computer operation: %w", err)
	}
	run := &servermodels.AgentRun{}
	if err := a.db.NewSelect().Model(run).Where("agr.workspace_id = ? AND agr.id = ?", computer.WorkspaceID, call.AgentRunID).Scan(ctx); err != nil {
		return conversationfile.RunAuthor{}, fmt.Errorf("load syncing computer operation run: %w", err)
	}
	return conversationfile.LoadRunAuthor(ctx, a.db, run)
}

// List 返回调用所属会话共享文件区中的全部文件及其当前内容文件，按路径排序。
func (a *SharedFilesAction) List(ctx context.Context, computer Identity, callID string) ([]SharedFile, error) {
	author, err := a.author(ctx, computer, callID)
	if err != nil {
		return nil, err
	}
	entries, err := a.store.List(ctx, computer.WorkspaceID, author.Area)
	if err != nil {
		return nil, err
	}
	ids := arr.Map(entries, func(entry conversationfile.Entry) string { return entry.FileID })
	contents := map[string]*servermodels.File{}
	if len(ids) > 0 {
		var records []*servermodels.File
		if err := a.db.NewSelect().Model(&records).Where("f.workspace_id = ? AND f.id IN (?)", computer.WorkspaceID, bun.List(ids)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("load shared file contents: %w", err)
		}
		for _, record := range records {
			contents[record.ID] = record
		}
	}
	files := make([]SharedFile, 0, len(entries))
	for _, entry := range entries {
		if content := contents[entry.FileID]; content != nil {
			files = append(files, SharedFile{Entry: entry, Content: content})
		}
	}
	return files, nil
}

// CreateUpload 为写回调用所属会话共享文件区中的一个文件创建内容上传，路径不合法时返回 conversationfile.ErrInvalidPath。
func (a *SharedFilesAction) CreateUpload(ctx context.Context, computer Identity, callID string, backend domain.FileStorageBackend, input SharedUploadInput) (*servermodels.File, error) {
	author, err := a.author(ctx, computer, callID)
	if err != nil {
		return nil, err
	}
	normalized, ok := domain.NormalizeConversationFilePath(input.Path)
	if !ok {
		return nil, conversationfile.ErrInvalidPath
	}
	return fileaction.CreateComputerUpload(ctx, a.db, computer.WorkspaceID, author.UserID, computer.ComputerID, backend, fileaction.UploadInput{
		FileName: path.Base(normalized), ContentType: input.ContentType, ByteSize: input.ByteSize,
	})
}

// Commit 核验已上传的内容并按同步基准写回调用所属会话共享文件区：文件在电脑上次同步后未被改动时写入原路径，否则另存为冲突副本；修改人为运行的 AI 员工。
func (a *SharedFilesAction) Commit(ctx context.Context, computer Identity, callID string, input SharedCommitInput, finalize fileaction.FinalizeFunc) (conversationfile.Entry, error) {
	author, err := a.author(ctx, computer, callID)
	if err != nil {
		return conversationfile.Entry{}, err
	}
	if _, err := fileaction.CompleteComputerUpload(ctx, a.db, computer.WorkspaceID, computer.ComputerID, input.FileID, finalize); err != nil {
		return conversationfile.Entry{}, err
	}
	return a.store.Adopt(ctx, conversationfile.WriteInput{
		WorkspaceID: computer.WorkspaceID, ConversationID: author.Area, Path: input.Path,
		Mode: conversationfile.WriteSynced, BaseHash: input.BaseHash, SubjectID: author.SubjectID, UserID: author.UserID,
	}, input.FileID)
}
