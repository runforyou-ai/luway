//go:build server

package direct

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationfileaction "github.com/runforyou-ai/luway/internal/actions/conversationfile"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// fileOps 持有文件存储的 Action、Query 和部署级存储配置。
type fileOps struct {
	createFileUpload   *fileaction.CreateUploadAction
	cancelFileUpload   *filemaintenance.CancelUploadAction
	completeFileUpload *fileaction.CompleteUploadAction
	getFile            *fileaction.GetQuery
	conversationFiles  *conversationfileaction.MemberFiles
	// computerSharedFiles 为电脑上执行中的命令与本机 Agent 轮次同步会话共享文件区。
	computerSharedFiles *computeraction.SharedFilesAction
	localFiles          *serverfilecontent.LocalStore
	s3                  serverfilecontent.S3Settings
	links               serverfilecontent.Links
	reader              *serverfilecontent.Reader
}

// newFileOps 创建文件存储的业务实现依赖，s3 返回部署当前的对象存储配置。
func newFileOps(db *bun.DB, localFiles *serverfilecontent.LocalStore, s3 serverfilecontent.S3Settings, links serverfilecontent.Links) *fileOps {
	reader := serverfilecontent.NewReader(localFiles, s3)
	store := conversationfileaction.NewStore(db, func() domain.FileStorageBackend { return s3().Backend() },
		serverfilecontent.NewWriter(localFiles, s3), reader)
	return &fileOps{
		createFileUpload:    fileaction.NewCreateUploadAction(db),
		cancelFileUpload:    filemaintenance.NewCancelUploadAction(db),
		completeFileUpload:  fileaction.NewCompleteUploadAction(db),
		getFile:             fileaction.NewGetQuery(db),
		conversationFiles:   conversationfileaction.NewMemberFiles(store),
		computerSharedFiles: computeraction.NewSharedFilesAction(db, store),
		localFiles:          localFiles,
		reader:              reader,
		s3:                  s3,
		links:               links,
	}
}

// CreateFileUpload 创建当前存储开关对应的文件上传请求；分片上传在对象存储中同时建立分片会话。
func (o *fileOps) CreateFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.FileUploadInput) (appservice.FileUpload, error) {
	record, err := o.createFileUpload.Execute(ctx, identity, o.s3().Backend(), fileaction.UploadInput{
		Purpose:     domain.FilePurpose(input.Purpose),
		FileName:    input.FileName,
		ContentType: input.ContentType,
		ByteSize:    input.ByteSize,
	})
	if err != nil {
		return appservice.FileUpload{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	contentURL, err := o.links.URL(domain.FileStorageBackend(record.StorageBackend), record.StorageKey)
	if err != nil {
		return appservice.FileUpload{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	if record.PartSize > 0 {
		if record.StorageBackend == string(domain.FileStorageBackendS3) {
			uploadID, err := serverfilecontent.CreateMultipart(ctx, o.s3(), record.StorageKey, record.ContentType)
			if err != nil {
				return appservice.FileUpload{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
			}
			stored, err := o.createFileUpload.SetMultipartUpload(ctx, identity, record.ID, uploadID)
			if err == nil && !stored {
				err = fileaction.ErrFileNotFound
			}
			if err != nil {
				// 分片会话未保存时立即清理远端会话。
				if cleanupErr := serverfilecontent.AbortMultipart(context.WithoutCancel(ctx), o.s3(), record.StorageKey, uploadID); cleanupErr != nil {
					slog.WarnContext(ctx, "清除未保存的分片会话失败", "file_id", record.ID, "error", cleanupErr)
				}
				return appservice.FileUpload{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
			}
		}
		return appservice.FileUpload{File: fileFromModel(record, contentURL), PartSize: record.PartSize}, nil
	}
	request, err := o.fileUploadRequest(ctx, meta, record, contentURL)
	if err != nil {
		return appservice.FileUpload{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	return appservice.FileUpload{File: fileFromModel(record, contentURL), Request: request}, nil
}

// CompleteFileUpload 核验文件内容并将上传标记为完成。
func (o *fileOps) CompleteFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fileID string) (appservice.File, error) {
	record, err := o.completeFileUpload.Execute(ctx, identity, fileID, o.finalizeFileContent)
	if err != nil {
		return appservice.File{}, fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	// 清除已合并的本地分片。
	if record.PartSize > 0 && record.StorageBackend == string(domain.FileStorageBackendLocal) {
		if err := o.localFiles.DeleteParts(record.StorageKey); err != nil {
			slog.WarnContext(ctx, "清理已合并分片失败", "file_id", record.ID, "error", err)
		}
	}
	contentURL, err := o.links.URL(domain.FileStorageBackend(record.StorageBackend), record.StorageKey)
	if err != nil {
		return appservice.File{}, fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, record.WorkspaceID), "文件上传已完成", "file_id", record.ID, "storage_backend", record.StorageBackend)
	return fileFromModel(record, contentURL), nil
}

// fileUploadRequest 返回本地上传地址或 S3 预签名请求。
func (o *fileOps) fileUploadRequest(ctx context.Context, meta appservice.RequestMeta, record *servermodels.File, contentURL string) (appservice.FileUploadRequest, error) {
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		return appservice.FileUploadRequest{
			Method: http.MethodPut, URL: contentURL,
			Headers: map[string]string{"Authorization": "Bearer " + meta.Token, "Content-Type": record.ContentType},
		}, nil
	}
	signed, err := serverfilecontent.PresignPut(ctx, o.s3(), record.StorageKey, record.ContentType, record.ByteSize)
	if err != nil {
		return appservice.FileUploadRequest{}, fmt.Errorf("presign S3 file upload: %w", err)
	}
	return appservice.FileUploadRequest{Method: signed.Method, URL: signed.URL, Headers: signed.Headers}, nil
}

// fileFieldKeys 把文件字段校验错误码映射为本地化文案键。
var fileFieldKeys = map[common.FieldCode]i18n.Key{
	fileaction.ValidationFileNameRequired:   i18n.FieldFileNameRequired,
	fileaction.ValidationContentTypeInvalid: i18n.FieldFileContentTypeInvalid,
	fileaction.ValidationByteSizeInvalid:    i18n.FieldFileByteSizeInvalid,
	fileaction.ValidationDocumentTooLarge:   i18n.FieldKnowledgeDocumentTooLarge,
	fileaction.ValidationPurposeInvalid:     i18n.FieldFilePurposeInvalid,
}

// fileOperationErrors 是文件校验和操作的错误转换规则。
var fileOperationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(fileFieldKeys),
	dispatch.Is(fileaction.ErrFileNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
})

// fileOperationError 转换文件校验和操作错误。
func fileOperationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return fileOperationErrors.Translate(meta, err, failureKey)
}

// fileFromModel 把存储文件转换为应用契约。
func fileFromModel(record *servermodels.File, contentURL string) appservice.File {
	return appservice.File{ID: record.ID, Name: record.OriginalName, ContentType: record.ContentType, ByteSize: record.ByteSize, ContentURL: contentURL}
}
