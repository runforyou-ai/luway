//go:build !server && (ios || android)

package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	mobilestorage "github.com/runforyou-ai/luway/internal/storage/mobile"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	mobileDatabaseName = "mobile.db"
	// mobileLocalDirectory 是存放本机数据库的目录，目录内容不进入系统备份。
	mobileLocalDirectory = "local"
)

// Open 初始化移动端使用的 SQLite 存储，数据库位于不参与系统备份的目录。
func Open(ctx context.Context) (*mobilestorage.Store, error) {
	directory := filepath.Join(application.Mobile.StoragePath(), mobileLocalDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create mobile data directory: %w", err)
	}
	if err := excludeFromBackup(directory); err != nil {
		return nil, fmt.Errorf("exclude mobile data directory from backup: %w", err)
	}
	return mobilestorage.Open(ctx, filepath.Join(directory, mobileDatabaseName))
}
