//go:build server

package filecontent

import (
	"context"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// Save 把服务端生成或导入的内容写入 S3。
func (s *Store) Save(ctx context.Context, record *servermodels.File, data []byte) (string, error) {
	return Put(ctx, s.s3(), record.StorageKey, record.ContentType, data)
}
