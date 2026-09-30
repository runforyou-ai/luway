//go:build !server && !ios && !android && !windows

package devicehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DocumentsDir 返回当前用户的文档目录：Linux 按 XDG 用户目录解析，其他平台为主目录下的 Documents。
func DocumentsDir() (string, error) {
	// Linux 的文档目录可由 XDG 用户目录配置改到其他位置或本地化名称。
	if runtime.GOOS == "linux" {
		if output, err := exec.Command("xdg-user-dir", "DOCUMENTS").Output(); err == nil {
			if dir := strings.TrimSpace(string(output)); dir != "" {
				return dir, nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Documents"), nil
}
