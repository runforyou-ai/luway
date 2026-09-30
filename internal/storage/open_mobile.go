//go:build !server && (ios || android)

package storage

import (
	"context"
	"path/filepath"

	mobilestorage "github.com/runforyou-ai/cervi/internal/storage/mobile"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const mobileDatabaseName = "mobile.db"

// Open 初始化移动端使用的 SQLite 存储。
func Open(ctx context.Context) (*mobilestorage.Store, error) {
	databasePath := filepath.Join(application.Mobile.StoragePath(), mobileDatabaseName)
	return mobilestorage.Open(ctx, databasePath)
}
