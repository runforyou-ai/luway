//go:build server

package filecontent

import (
	"bytes"
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// Writer 按文件记录中固定的存储类型写入服务端导入内容。
type Writer struct {
	local *LocalStore
	s3    S3Config
}

// NewWriter 创建本地和对象存储文件写入器。
func NewWriter(local *LocalStore, s3 S3Config) *Writer {
	return &Writer{local: local, s3: s3}
}

// Save 写入文件并返回对象存储 ETag。
func (w *Writer) Save(ctx context.Context, record *servermodels.File, data []byte) (string, error) {
	switch domain.FileStorageBackend(record.StorageBackend) {
	case domain.FileStorageBackendLocal:
		if err := w.local.Save(ctx, record.StorageKey, bytes.NewReader(data), record.ByteSize); err != nil {
			return "", err
		}
		return "", nil
	case domain.FileStorageBackendS3:
		return Put(ctx, w.s3, record.StorageKey, record.ContentType, data)
	default:
		return "", fmt.Errorf("invalid file storage backend %q", record.StorageBackend)
	}
}
