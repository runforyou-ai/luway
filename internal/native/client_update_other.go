//go:build !server && (ios || android)

package native

import "github.com/wailsapp/wails/v3/pkg/application"

// NewClientUpdater 在移动端禁用从服务器更新客户端，移动端经应用商店更新。
func NewClientUpdater(*application.App) ClientUpdater {
	return nil
}
