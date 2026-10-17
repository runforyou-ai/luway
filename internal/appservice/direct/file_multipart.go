//go:build server

package direct

import (
	"context"
	"net/http"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// SignFileUpload 只为当前上传者未过期的文件签发受限 S3 操作。
func (o *fileOps) SignFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fileID string, input appservice.FileUploadSignInput) (appservice.FileUploadRequest, error) {
	record, err := o.getFile.Execute(ctx, identity, fileID)
	if err == nil && (record.CreatedByUserID != identity.User.ID || record.Status != string(domain.FileStatusPending) || record.Expired) {
		err = fileaction.ErrFileNotFound
	}
	if err != nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	var signed serverfilecontent.SignedRequest
	if record.PartSize == 0 {
		if input.Method != http.MethodPut || input.UploadID != "" || input.PartNumber != 0 {
			return appservice.FileUploadRequest{}, fileOperationError(meta, fileaction.ErrFileNotFound, i18n.ErrorFileUploadCreateFailed)
		}
		signed, err = serverfilecontent.PresignPut(ctx, o.s3(), record.StorageKey, record.ContentType, record.ByteSize)
	} else {
		if record.MultipartUploadID == nil || input.UploadID != *record.MultipartUploadID {
			return appservice.FileUploadRequest{}, fileOperationError(meta, fileaction.ErrFileNotFound, i18n.ErrorFileUploadCreateFailed)
		}
		if input.Method == http.MethodPut {
			var size int64
			size, err = fileaction.UploadPartSize(record, input.PartNumber)
			if err == nil {
				signed, err = serverfilecontent.PresignPart(ctx, o.s3(), record.StorageKey, input.UploadID, input.PartNumber, size)
			}
		} else if input.PartNumber == 0 && (input.Method == http.MethodGet || input.Method == http.MethodPost || input.Method == http.MethodDelete) {
			signed, err = serverfilecontent.PresignMultipartControl(ctx, o.s3(), record.StorageKey, input.UploadID, input.Method)
		} else {
			err = fileaction.ErrFileNotFound
		}
	}
	if err != nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	return appservice.FileUploadRequest{Method: signed.Method, URL: signed.URL, Headers: signed.Headers}, nil
}

// finalizeFileContent 核验客户端已经完成合并的对象，重试不再重复合并。
func (o *fileOps) finalizeFileContent(ctx context.Context, record *servermodels.File) (serverfilecontent.UploadedObject, error) {
	return o.reader.Inspect(ctx, record)
}

// CancelFileUpload 将当前用户取消的临时文件交给过期清理。
func (o *fileOps) CancelFileUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fileID string) error {
	if _, err := o.getFile.Execute(ctx, identity, fileID); err != nil {
		return fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	if err := o.cancelFileUpload.Execute(ctx, identity, fileID); err != nil {
		return fileOperationError(meta, err, i18n.ErrorFileUploadCompleteFailed)
	}
	return nil
}
