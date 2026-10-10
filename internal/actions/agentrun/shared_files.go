//go:build server

package agentrun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// DocumentConverter 把 PDF、Word 等文档转换为文本，charset 为空时自动判定文本编码。
type DocumentConverter interface {
	Convert(ctx context.Context, name, charset string, source io.Reader) (string, error)
}

// loadRunSharedFiles 按运行所属会话确定共享文件区，以 AI 员工的聊天主体为修改人，以配置版本的创建人为内容文件的上传用户。
func loadRunSharedFiles(ctx context.Context, db bun.IDB, store *conversationfile.Store, documents DocumentConverter, run *servermodels.AgentRun) (*runSharedFiles, error) {
	author, err := conversationfile.LoadRunAuthor(ctx, db, run)
	if err != nil {
		return nil, err
	}
	return &runSharedFiles{store: store, documents: documents, workspaceID: run.WorkspaceID, area: author.Area, conversationID: run.ConversationID,
		subjectID: author.SubjectID, userID: author.UserID}, nil
}

// runSharedFiles 是一次运行使用的会话共享文件区：area 是文件区所属会话，conversationID 是运行所属会话，附件从运行所属会话中读取。
type runSharedFiles struct {
	store          *conversationfile.Store
	documents      DocumentConverter
	workspaceID    string
	area           string
	conversationID string
	subjectID      string
	userID         string
}

// sharedFileErrors 把文件区的错误转换为交给模型的原因。
var sharedFileErrors = []struct {
	err     error
	message string
}{
	{conversationfile.ErrNotFound, "共享文件区中没有这个文件，用 list_shared_files 查看已有文件。"},
	{conversationfile.ErrInvalidPath, "路径不合法：使用 shared/ 下的相对路径，不能包含 .. 越出共享文件区。"},
	{conversationfile.ErrExists, "共享文件区中已有同名文件，换一个路径。"},
	{conversationfile.ErrNotRead, "文件已存在，覆盖或修改前先用 read_file 读取。"},
	{conversationfile.ErrModified, "文件在读取后已被改动，请重新用 read_file 读取后再改。"},
}

// modelError 返回交给模型的原因，未预期的错误原样返回。
func modelError(err error) error {
	for _, item := range sharedFileErrors {
		if errors.Is(err, item.err) {
			return errors.New(item.message)
		}
	}
	return err
}

// sharedFile 把文件区中的文件转换为运行期的共享文件。
func sharedFile(entry conversationfile.Entry) agentruntime.SharedFile {
	return agentruntime.SharedFile{Path: entry.Path, ContentType: entry.ContentType, ByteSize: entry.ByteSize, Hash: entry.ContentHash, UpdatedAt: entry.UpdatedAt, UpdatedBy: support.Deref(entry.UpdatedByName)}
}

// List 返回文件区中的全部文件。
func (f *runSharedFiles) List(ctx context.Context) ([]agentruntime.SharedFile, error) {
	entries, err := f.store.List(ctx, f.workspaceID, f.area)
	if err != nil {
		return nil, err
	}
	return arr.OrEmpty(arr.Map(entries, sharedFile)), nil
}

// Read 读取文件：超过读取上限时不带内容，PDF、Word 等文档转换为只读文本。
func (f *runSharedFiles) Read(ctx context.Context, filePath string) (agentruntime.SharedFileContent, error) {
	content, err := f.store.Read(ctx, f.workspaceID, f.area, filePath, textfile.MaxReadBytes)
	if err != nil {
		return agentruntime.SharedFileContent{}, modelError(err)
	}
	result := agentruntime.SharedFileContent{SharedFile: sharedFile(content.Entry), Data: content.Data}
	// PDF 与 Office 文档转换为只读文本，其余文件按原内容提供。
	if content.Data != nil && domain.BinaryDocument(content.Path) {
		text, err := f.documents.Convert(ctx, content.Path, "", bytes.NewReader(content.Data))
		if err != nil {
			slog.WarnContext(logscope.WithWorkspace(ctx, f.workspaceID), "会话共享文件转换为文本失败", "conversation_id", f.area, "error", err)
		} else {
			result.Text = text
		}
	}
	return result, nil
}

// Write 以完整内容创建或覆盖文件。
func (f *runSharedFiles) Write(ctx context.Context, path string, content []byte, baseHash string) (agentruntime.SharedFile, error) {
	entry, err := f.store.Write(ctx, conversationfile.WriteInput{
		WorkspaceID: f.workspaceID, ConversationID: f.area, Path: path, Data: content,
		Mode: conversationfile.WriteReplace, BaseHash: baseHash, SubjectID: f.subjectID, UserID: f.userID,
	})
	if err != nil {
		return agentruntime.SharedFile{}, modelError(err)
	}
	return sharedFile(entry), nil
}

// SaveAttachment 把运行所属会话中的附件保存为文件区中的新文件。
func (f *runSharedFiles) SaveAttachment(ctx context.Context, messageID, path string) (agentruntime.SharedFile, error) {
	entry, err := f.store.SaveAttachment(ctx, conversationfile.WriteInput{
		WorkspaceID: f.workspaceID, ConversationID: f.area, Path: path, Mode: conversationfile.WriteNew, SubjectID: f.subjectID, UserID: f.userID,
	}, f.conversationID, messageID)
	if err != nil {
		return agentruntime.SharedFile{}, modelError(err)
	}
	return sharedFile(entry), nil
}
