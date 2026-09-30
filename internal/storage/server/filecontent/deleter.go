//go:build server

package filecontent

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// Deleter 删除文件记录指向的内容。
type Deleter struct {
	local *LocalStore
	s3    S3Config
}

// NewDeleter 创建文件内容删除器。
func NewDeleter(local *LocalStore, s3 S3Config) *Deleter {
	return &Deleter{local: local, s3: s3}
}

// Delete 按文件记录中的存储类型删除内容。
func (d *Deleter) Delete(ctx context.Context, record *servermodels.File) error {
	switch domain.FileStorageBackend(record.StorageBackend) {
	case domain.FileStorageBackendLocal:
		if err := d.local.DeleteParts(record.StorageKey); err != nil {
			return err
		}
		return d.local.Delete(ctx, record.StorageKey)
	case domain.FileStorageBackendS3:
		if record.MultipartUploadID != nil {
			if err := AbortMultipart(ctx, d.s3, record.StorageKey, *record.MultipartUploadID); err != nil {
				return err
			}
		}
		return Delete(ctx, d.s3, record.StorageKey)
	default:
		return fmt.Errorf("invalid file storage backend %q", record.StorageBackend)
	}
}
