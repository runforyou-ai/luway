//go:build !server && (ios || android)

// Package mobile 管理移动端的 SQLite 存储。
package mobile

import (
	"context"
	"embed"

	"github.com/runforyou-ai/luway/internal/storage/native"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store 管理移动端的 SQLite 存储，登录凭据与服务器地址的读写由共用的原生端存储提供。
type Store struct {
	*native.Store
}

// Open 创建移动端 SQLite 数据库并执行移动端迁移。
func Open(ctx context.Context, databasePath string) (*Store, error) {
	store, err := native.Open(ctx, databasePath, migrationFiles, "移动端")
	if err != nil {
		return nil, err
	}
	return &Store{Store: store}, nil
}
