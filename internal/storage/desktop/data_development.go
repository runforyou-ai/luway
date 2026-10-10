//go:build !server && !ios && !android && !production

package desktop

import (
	"fmt"
	"path/filepath"
)

// DataDirectory 返回桌面端数据目录的绝对路径：工作目录下的 data/desktop。
func DataDirectory() (string, error) {
	directory, err := filepath.Abs(filepath.Join("data", "desktop"))
	if err != nil {
		return "", fmt.Errorf("resolve desktop data directory: %w", err)
	}
	return directory, nil
}
