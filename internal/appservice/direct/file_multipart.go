//go:build server

package direct

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// CreateFilePartUpload 为当前用户的有效临时文件签发分片直传请求。
func (o *fileOps) CreateFilePartUpload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, fileID string, input appservice.FilePartUploadInput) (appservice.FileUploadRequest, error) {
	record, err := o.getFile.Execute(ctx, identity, fileID)
	if err == nil && (record.CreatedByUserID != identity.User.ID || record.Status != string(domain.FileStatusPending) || record.Expired) {
		err = fileaction.ErrFileNotFound
	}
	if err != nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	size, err := fileaction.UploadPartSize(record, input.PartNumber)
	if err != nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		contentURL, err := o.links.URL(domain.FileStorageBackendLocal, record.StorageKey)
		if err != nil {
			return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
		}
		return appservice.FileUploadRequest{Method: http.MethodPut, URL: contentURL + "?partNumber=" + strconv.Itoa(int(input.PartNumber)),
			Headers: map[string]string{"Authorization": "Bearer " + meta.Token}}, nil
	}
	if record.MultipartUploadID == nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, fileaction.ErrFileNotFound, i18n.ErrorFileUploadCreateFailed)
	}
	request, err := serverfilecontent.PresignPart(ctx, o.s3(), record.StorageKey, *record.MultipartUploadID, input.PartNumber, size)
	if err != nil {
		return appservice.FileUploadRequest{}, fileOperationError(meta, err, i18n.ErrorFileUploadCreateFailed)
	}
	return appservice.FileUploadRequest{Method: request.Method, URL: request.URL, Headers: request.Headers}, nil
}

// finalizeFileContent 合并尚未完成的分片并读取最终对象的元数据与开头字节。
func (o *fileOps) finalizeFileContent(ctx context.Context, record *servermodels.File) (serverfilecontent.UploadedObject, error) {
	if record.PartSize > 0 {
		if record.StorageBackend == string(domain.FileStorageBackendLocal) {
			if err := o.localFiles.CompleteMultipart(ctx, record.StorageKey, record.ByteSize, record.PartSize); err != nil {
				return serverfilecontent.UploadedObject{}, err
			}
		} else {
			if record.MultipartUploadID == nil {
				return serverfilecontent.UploadedObject{}, fileaction.ErrFileNotFound
			}
			err := serverfilecontent.CompleteMultipart(ctx, o.s3(), record.StorageKey, *record.MultipartUploadID, record.ByteSize, record.PartSize)
			var missing *types.NoSuchUpload
			// 合并响应丢失后，最终对象用于确认上一次合并结果。
			if err != nil && !errors.As(err, &missing) {
				return serverfilecontent.UploadedObject{}, fmt.Errorf("complete multipart upload: %w", err)
			}
		}
	}
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
