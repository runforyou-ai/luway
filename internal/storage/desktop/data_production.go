//go:build !server && !ios && !android && production

package desktop

import (
	"path/filepath"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// DataDirectory 返回桌面端数据目录的绝对路径：用户数据目录下以品牌标识命名的目录。
func DataDirectory() (string, error) {
	return filepath.Join(application.Path(application.PathDataHome), brand.Build().Slug), nil
}
