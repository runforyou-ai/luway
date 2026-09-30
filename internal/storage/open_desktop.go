//go:build !server && !ios && !android

package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runforyou-ai/cervi/internal/common/brand"
	desktopstorage "github.com/runforyou-ai/cervi/internal/storage/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const desktopDatabaseName = "desktop.db"

// Open 初始化桌面端使用的 SQLite 存储。
func Open(ctx context.Context) (*desktopstorage.Store, error) {
	dataDirectory, err := DesktopDataDirectory()
	if err != nil {
		return nil, err
	}
	return desktopstorage.Open(ctx, filepath.Join(dataDirectory, desktopDatabaseName))
}

// DesktopDataDirectory 返回桌面端数据目录的绝对路径：设置 DESKTOP_DATA_DIR 时使用该目录，否则为用户数据目录下以品牌标识命名的目录。
func DesktopDataDirectory() (string, error) {
	dataDirectory := strings.TrimSpace(os.Getenv("DESKTOP_DATA_DIR"))
	if dataDirectory == "" {
		dataDirectory = filepath.Join(application.Path(application.PathDataHome), brand.Build().Slug)
	}
	absoluteDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve desktop data directory: %w", err)
	}
	return absoluteDirectory, nil
}
