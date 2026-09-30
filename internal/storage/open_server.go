//go:build server

package storage

import (
	"context"

	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
)

// Open 初始化服务端使用的 PostgreSQL 存储。
func Open(ctx context.Context, config serverconfig.DatabaseConfig) (*serverstorage.Store, error) {
	return serverstorage.Open(ctx, config)
}
