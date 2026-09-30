//go:build !server && windows

package devicehost

import "golang.org/x/sys/windows"

// DocumentsDir 返回当前用户的文档目录，包含被 OneDrive 等重定向后的实际位置。
func DocumentsDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
}
