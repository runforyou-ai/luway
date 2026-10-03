//go:build !server && windows

package native

import (
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"golang.org/x/sys/windows/registry"
)

// syncInstalledVersion 把当前版本写入安装程序登记的卸载项（用户范围在 HKCU，机器范围在 HKLM），使系统“应用”列表显示应用内更新后的版本；未经安装程序安装或无权写入时跳过。
func syncInstalledVersion() {
	value := brand.Build()
	path := `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + value.Company + value.DisplayName()
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			continue
		}
		if installed, _, err := key.GetStringValue("DisplayVersion"); err != nil || installed != buildinfo.Version {
			if err := key.SetStringValue("DisplayVersion", buildinfo.Version); err != nil {
				slog.Warn("更新已安装版本失败", "error", err)
			}
		}
		key.Close()
	}
}
