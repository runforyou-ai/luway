//go:build server

package integrationtest

import (
	"context"
	"sync"

	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

var sharedTestDatabase struct {
	once sync.Once
	err  error
}

// openSharedTestDatabase 首次连接时迁移本测试进程的共享测试库，每次调用创建由调用方关闭的独立连接池。
func openSharedTestDatabase(ctx context.Context, config serverconfig.DatabaseConfig) (*serverstorage.Store, error) {
	sharedTestDatabase.once.Do(func() {
		store, err := serverstorage.Open(ctx, config, serverstorage.CoreMigrations())
		if err != nil {
			sharedTestDatabase.err = err
			return
		}
		sharedTestDatabase.err = store.Close()
	})
	if sharedTestDatabase.err != nil {
		return nil, sharedTestDatabase.err
	}
	return serverstorage.Connect(ctx, config, serverstorage.CoreMigrations(), "", 0)
}
