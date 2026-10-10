//go:build server

package integrationtest

import (
	"github.com/runforyou-ai/luway/internal/domain"
)

// localStorage 返回新文件写入本地存储。
func localStorage() domain.FileStorageBackend {
	return domain.FileStorageBackendLocal
}
