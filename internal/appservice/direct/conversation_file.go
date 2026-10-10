//go:build server

package direct

import (
	"context"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	conversationfileaction "github.com/runforyou-ai/luway/internal/actions/conversationfile"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// ListConversationFiles 返回会话共享文件区中的文件。
func (o *fileOps) ListConversationFiles(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ConversationFileList, error) {
	entries, err := o.conversationFiles.List(ctx, identity, conversationID)
	if err != nil {
		return appservice.ConversationFileList{}, conversationFileError(meta, err, i18n.ErrorConversationFilesLoadFailed)
	}
	return appservice.ConversationFileList{Items: arr.Map(entries, conversationFileFromEntry)}, nil
}

// AddConversationFile 把上传完成的文件加入会话共享文件区。
func (o *fileOps) AddConversationFile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.AddConversationFileInput) (appservice.ConversationFile, error) {
	entry, err := o.conversationFiles.AddUpload(ctx, identity, conversationID, input.FileID)
	if err != nil {
		return appservice.ConversationFile{}, conversationFileError(meta, err, i18n.ErrorConversationFileSaveFailed)
	}
	return conversationFileFromEntry(entry), nil
}

// SaveConversationAttachment 把消息附件保存到会话共享文件区。
func (o *fileOps) SaveConversationAttachment(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, messageID string) (appservice.ConversationFile, error) {
	entry, err := o.conversationFiles.SaveAttachment(ctx, identity, conversationID, messageID)
	if err != nil {
		return appservice.ConversationFile{}, conversationFileError(meta, err, i18n.ErrorConversationFileSaveFailed)
	}
	return conversationFileFromEntry(entry), nil
}

// DeleteConversationFile 从会话共享文件区删除文件。
func (o *fileOps) DeleteConversationFile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, fileID string) error {
	if err := o.conversationFiles.Delete(ctx, identity, conversationID, fileID); err != nil {
		return conversationFileError(meta, err, i18n.ErrorConversationFileDeleteFailed)
	}
	return nil
}

// GetConversationFileDownload 签发会话共享文件区中文件当前版本的下载地址。
func (o *fileOps) GetConversationFileDownload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, fileID string) (appservice.FileDownload, error) {
	_, record, err := o.conversationFiles.File(ctx, identity, conversationID, fileID)
	if err != nil {
		return appservice.FileDownload{}, conversationFileError(meta, err, i18n.ErrorFileNotFound)
	}
	return o.fileDownload(ctx, meta, record)
}

// conversationFileErrors 是会话文件区的错误转换规则。
var conversationFileErrors = dispatch.Catalogs(dispatch.Catalog{
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.Is(conversationfileaction.ErrNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.Is(conversationfileaction.ErrInvalidPath, dispatch.Invalid(i18n.ErrorConversationFileNameInvalid)),
}, dispatch.CommonErrors)

// conversationFileError 把会话文件区的错误转换为本地化错误，未预期的失败使用 failureKey。
func conversationFileError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return conversationFileErrors.Translate(meta, err, failureKey)
}

// conversationFileFromEntry 把会话文件转换为应用契约。
func conversationFileFromEntry(entry conversationfileaction.Entry) appservice.ConversationFile {
	return appservice.ConversationFile{
		ID: entry.ID, Path: entry.Path, ContentType: entry.ContentType, ByteSize: entry.ByteSize, UpdatedAt: entry.UpdatedAt,
		UpdatedByName: support.Deref(entry.UpdatedByName),
	}
}
