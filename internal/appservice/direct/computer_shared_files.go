//go:build server

package direct

import (
	"context"
	"errors"
	"mime"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationfileaction "github.com/runforyou-ai/luway/internal/actions/conversationfile"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
)

// ListComputerSharedFiles 返回本电脑执行中的操作所属会话共享文件区的文件清单：本地存储给出服务端相对的内容地址，对象存储给出预签名下载地址。
func (o *fileOps) ListComputerSharedFiles(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string) (appservice.ComputerSharedFileList, error) {
	files, err := o.computerSharedFiles.List(ctx, computer, operationID)
	if err != nil {
		return appservice.ComputerSharedFileList{}, computerSharedError(meta, err)
	}
	output := appservice.ComputerSharedFileList{Files: make([]appservice.ComputerSharedFile, 0, len(files))}
	for _, file := range files {
		url, err := o.links.URL(domain.FileStorageBackend(file.Content.StorageBackend), file.Content.StorageKey)
		if err == nil && file.Content.StorageBackend == string(domain.FileStorageBackendS3) {
			var signed serverfilecontent.SignedRequest
			signed, err = serverfilecontent.PresignDownload(ctx, o.s3(), file.Content.StorageKey, mime.FormatMediaType("attachment", map[string]string{"filename": file.Content.OriginalName}))
			url = signed.URL
		}
		if err != nil {
			return appservice.ComputerSharedFileList{}, computerRequestError(meta, err)
		}
		output.Files = append(output.Files, appservice.ComputerSharedFile{Path: file.Path, Hash: file.ContentHash, ByteSize: file.ByteSize, URL: url})
	}
	return output, nil
}

// CreateComputerSharedUpload 为写回会话共享文件区中的一个文件创建整体上传：本地存储以电脑凭据写入服务端，对象存储使用预签名请求直传。
func (o *fileOps) CreateComputerSharedUpload(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerSharedUploadInput) (appservice.ComputerSharedUpload, error) {
	record, err := o.computerSharedFiles.CreateUpload(ctx, computer, operationID, o.s3().Backend(), computeraction.SharedUploadInput{
		Path: input.Path, ContentType: input.ContentType, ByteSize: input.ByteSize,
	})
	if err != nil {
		return appservice.ComputerSharedUpload{}, computerSharedError(meta, err)
	}
	contentURL, err := o.links.URL(domain.FileStorageBackend(record.StorageBackend), record.StorageKey)
	if err != nil {
		return appservice.ComputerSharedUpload{}, computerRequestError(meta, err)
	}
	request, err := o.fileUploadRequest(ctx, meta, record, contentURL)
	if err != nil {
		return appservice.ComputerSharedUpload{}, computerRequestError(meta, err)
	}
	return appservice.ComputerSharedUpload{FileID: record.ID, Request: request}, nil
}

// CommitComputerSharedFile 核验上传完成的内容并按同步基准写回会话共享文件区。
func (o *fileOps) CommitComputerSharedFile(ctx context.Context, meta appservice.RequestMeta, computer computeraction.Identity, operationID string, input appservice.ComputerSharedCommitInput) (appservice.ComputerSharedCommit, error) {
	entry, err := o.computerSharedFiles.Commit(ctx, computer, operationID, computeraction.SharedCommitInput{
		Path: input.Path, FileID: input.FileID, BaseHash: input.BaseHash,
	}, o.finalizeFileContent)
	if err != nil {
		return appservice.ComputerSharedCommit{}, computerSharedError(meta, err)
	}
	return appservice.ComputerSharedCommit{Path: entry.Path, Hash: entry.ContentHash}, nil
}

// computerSharedErrors 是电脑同步会话共享文件区的错误转换规则。
var computerSharedErrors = dispatch.Catalog{
	dispatch.Is(computeraction.ErrSyncUnavailable, dispatch.Conflict(i18n.ErrorComputerRequestFailed, "shared_sync_unavailable")),
	dispatch.Is(conversationfileaction.ErrInvalidPath, dispatch.Invalid(i18n.ErrorConversationFileNameInvalid)),
	dispatch.Is(fileaction.ErrFileNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.Is(conversationfileaction.ErrNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	// 文件内容校验失败时返回不带字段的校验失败。
	func(meta appservice.RequestMeta, err error) error {
		if _, ok := errors.AsType[*fileaction.ValidationError](err); ok {
			return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		return nil
	},
}

// computerSharedError 转换电脑同步会话共享文件区的错误：操作已不能同步时返回冲突，路径或内容不合法时返回校验失败，内容文件不存在时返回未找到。
func computerSharedError(meta appservice.RequestMeta, err error) error {
	return computerSharedErrors.Translate(meta, err, i18n.ErrorComputerRequestFailed)
}
