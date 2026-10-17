//go:build server

package filecontent

import (
	"context"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// Delete 幂等清理未完成的分片会话与对象。
func (s *Store) Delete(ctx context.Context, record *servermodels.File) error {
	if record.MultipartUploadID != nil {
		if err := AbortMultipart(ctx, s.s3(), record.StorageKey, *record.MultipartUploadID); err != nil {
			return err
		}
	}
	return Delete(ctx, s.s3(), record.StorageKey)
}
