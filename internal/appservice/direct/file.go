//go:build server

package direct

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
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
	localFiles         *serverfilecontent.LocalStore
	s3                 serverfilecontent.S3Config
	links              serverfilecontent.Links
}

// newFileOps 创建文件存储的业务实现依赖。
func newFileOps(db *bun.DB, localFiles *serverfilecontent.LocalStore, s3 serverfilecontent.S3Config, links serverfilecontent.Links) fileOps {
	return fileOps{
		createFileUpload:   fileaction.NewCreateUploadAction(db),
		cancelFileUpload:   filemaintenance.NewCancelUploadAction(db),
		completeFileUpload: fileaction.NewCompleteUploadAction(db),
		getFile:            fileaction.NewGetQuery(db),
		localFiles:         localFiles,
		s3:                 s3,
		links:              links,
	}
}

// CreateFileUpload 创建当前存储开关对应的文件上传请求；分片上传在对象存储中同时建立分片会话。
func (o *directOperations) CreateFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.FileUploadInput) (appservice.FileUpload, error) {
	record, err := o.createFileUpload.Execute(ctx, identity, o.s3.Backend(), fileaction.UploadInput{
		Purpose:     domain.FilePurpose(input.Purpose),
		FileName:    input.FileName,
		ContentType: input.ContentType,
		ByteSize:    input.ByteSize,
	})
	if err != nil {
		return appservice.FileUpload{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	contentURL, err := o.links.URL(domain.FileStorageBackend(record.StorageBackend), record.StorageKey)
	if err != nil {
		return appservice.FileUpload{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	if record.PartSize > 0 {
		if record.StorageBackend == string(domain.FileStorageBackendS3) {
			uploadID, err := serverfilecontent.CreateMultipart(ctx, o.s3, record.StorageKey, record.ContentType)
			if err != nil {
				return appservice.FileUpload{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
			}
			stored, err := o.createFileUpload.SetMultipartUpload(ctx, identity, record.ID, uploadID)
			if err == nil && !stored {
				err = fileaction.ErrFileNotFound
			}
			if err != nil {
				// 分片会话未保存时立即清理远端会话。
				if cleanupErr := serverfilecontent.AbortMultipart(context.WithoutCancel(ctx), o.s3, record.StorageKey, uploadID); cleanupErr != nil {
					slog.Warn("清除未保存的分片会话失败", "file_id", record.ID, "error", cleanupErr)
				}
				return appservice.FileUpload{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
			}
		}
		return appservice.FileUpload{File: fileFromModel(record, contentURL), PartSize: record.PartSize}, nil
	}
	request, err := o.fileUploadRequest(ctx, meta, record, contentURL)
	if err != nil {
		return appservice.FileUpload{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	return appservice.FileUpload{File: fileFromModel(record, contentURL), Request: request}, nil
}

// CompleteFileUpload 核验文件内容并将上传标记为完成。
func (o *directOperations) CompleteFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fileID string) (appservice.File, error) {
	record, err := o.completeFileUpload.Execute(ctx, identity, fileID, o.finalizeFileContent)
	if err != nil {
		return appservice.File{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	o.cleanupCompletedParts(record)
	return o.completedFile(meta, record)
}

// completedFile 为已完成的上传生成文件地址并记录结果。
func (o *directOperations) completedFile(meta appservice.RequestMeta, record *servermodels.File) (appservice.File, error) {
	contentURL, err := o.links.URL(domain.FileStorageBackend(record.StorageBackend), record.StorageKey)
	if err != nil {
		return appservice.File{}, o.fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	slog.Info("文件上传已完成", "organization_id", record.OrganizationID, "file_id", record.ID, "storage_backend", record.StorageBackend)
	return fileFromModel(record, contentURL), nil
}

// fileUploadRequest 返回本地上传地址或 S3 预签名请求。
func (o *directOperations) fileUploadRequest(ctx context.Context, meta appservice.RequestMeta, record *servermodels.File, contentURL string) (appservice.FileUploadRequest, error) {
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		return appservice.FileUploadRequest{
			Method: http.MethodPut, URL: contentURL,
			Headers: map[string]string{"Authorization": "Bearer " + meta.Token, "Content-Type": record.ContentType},
		}, nil
	}
	signed, err := serverfilecontent.PresignPut(ctx, o.s3, record.StorageKey, record.ContentType)
	if err != nil {
		return appservice.FileUploadRequest{}, fmt.Errorf("presign S3 file upload: %w", err)
	}
	return appservice.FileUploadRequest{Method: signed.Method, URL: signed.URL, Headers: signed.Headers}, nil
}

// statFile 按文件记录的存储类型核验内容。
func (o *directOperations) statFile(ctx context.Context, record *servermodels.File) (string, int64, error) {
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		info, err := o.localFiles.Stat(ctx, record.StorageKey)
		if err != nil {
			return "", 0, fmt.Errorf("stat local file: %w", err)
		}
		return "", info.Size(), nil
	}
	info, err := serverfilecontent.Stat(ctx, o.s3, record.StorageKey)
	if err != nil {
		return "", 0, fmt.Errorf("stat S3 file: %w", err)
	}
	return info.ETag, info.ByteSize, nil
}

// fileOperationError 转换文件校验和操作错误。
func (o *directOperations) fileOperationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 映射文件字段校验文案。
		keys := map[common.FieldCode]i18n.Key{
			fileaction.ValidationFileNameRequired:   i18n.FieldFileNameRequired,
			fileaction.ValidationContentTypeInvalid: i18n.FieldFileContentTypeInvalid,
			fileaction.ValidationByteSizeInvalid:    i18n.FieldFileByteSizeInvalid,
			fileaction.ValidationDocumentTooLarge:   i18n.FieldKnowledgeDocumentTooLarge,
			fileaction.ValidationPurposeInvalid:     i18n.FieldFilePurposeInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, fileaction.ErrFileNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	return appservice.FailedError(meta, failureKey, err)
}

// fileFromModel 把存储文件转换为应用契约。
func fileFromModel(record *servermodels.File, contentURL string) appservice.File {
	return appservice.File{ID: record.ID, Name: record.OriginalName, ContentType: record.ContentType, ByteSize: record.ByteSize, ContentURL: contentURL}
}

// cleanupCompletedParts 清除已经合并且已确认完成的本地分片。
func (o *directOperations) cleanupCompletedParts(record *servermodels.File) {
	if record.PartSize > 0 && record.StorageBackend == string(domain.FileStorageBackendLocal) {
		if err := o.localFiles.DeleteParts(record.StorageKey); err != nil {
			slog.Warn("清理已合并分片失败", "file_id", record.ID, "error", err)
		}
	}
}
