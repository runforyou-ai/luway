//go:build server

package file

import (
	"context"
	"errors"
	"strconv"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

var (
	// ErrUploadNotPending 表示文件不是待写入内容的本地文件或已过期。
	ErrUploadNotPending = errors.New("file upload not pending")
	// ErrUploadPartInvalid 表示分片序号不合法。
	ErrUploadPartInvalid = errors.New("file upload part invalid")
)

// LocalUpload 定义一次通过校验的本地对象写入；分片文件的 PartNumber 大于 0，ExpectedSize 为本次请求应写入的字节数。
type LocalUpload struct {
	FileID       string
	PartNumber   int32
	ExpectedSize int64
}

// AuthorizeMemberLocalUpload 校验成员写入本地对象：文件须由该成员账号创建且待写入内容，partNumber 为分片文件的序号参数。
func (q *GetQuery) AuthorizeMemberLocalUpload(ctx context.Context, identity *servermodels.Identity, storageKey, partNumber string) (LocalUpload, error) {
	record, err := q.ExecuteByStorageKey(ctx, identity, storageKey)
	if err != nil {
		return LocalUpload{}, err
	}
	if record.CreatedByUserID != identity.User.ID {
		return LocalUpload{}, ErrFileNotFound
	}
	return localUpload(record, partNumber)
}

// AuthorizeVisitorLocalUpload 校验网站访客写入本地对象：文件须属于 organizationID 下该渠道外部编号的访客且待写入内容，organizationID 为空时不限定工作区。
func (q *GetQuery) AuthorizeVisitorLocalUpload(ctx context.Context, organizationID, externalID, storageKey, partNumber string) (LocalUpload, error) {
	record, err := q.VisitorPendingByStorageKey(ctx, externalID, storageKey)
	if err != nil {
		return LocalUpload{}, err
	}
	if organizationID != "" && record.OrganizationID != organizationID {
		return LocalUpload{}, ErrFileNotFound
	}
	return localUpload(record, partNumber)
}

// localUpload 校验文件为未过期的待写入本地文件，并按分片序号计算本次应写入的字节数。
func localUpload(record *servermodels.File, partNumber string) (LocalUpload, error) {
	if record.StorageBackend != string(domain.FileStorageBackendLocal) || record.Status != string(domain.FileStatusPending) || record.Expired {
		return LocalUpload{}, ErrUploadNotPending
	}
	upload := LocalUpload{FileID: record.ID, ExpectedSize: record.ByteSize}
	if record.PartSize <= 0 {
		return upload, nil
	}
	number, err := strconv.ParseInt(partNumber, 10, 32)
	if err != nil {
		return LocalUpload{}, ErrUploadPartInvalid
	}
	size, err := UploadPartSize(record, int32(number))
	if err != nil {
		return LocalUpload{}, ErrUploadPartInvalid
	}
	upload.PartNumber, upload.ExpectedSize = int32(number), size
	return upload, nil
}
