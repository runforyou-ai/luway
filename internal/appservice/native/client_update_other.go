//go:build !server && (ios || android || (linux && !android))

package native

import (
	"context"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewClientUpdater 在移动端与 Linux 上禁用从服务器更新客户端，移动端经应用商店更新，Linux 由使用者手动安装新版本。
func NewClientUpdater(*application.App, func(context.Context) (string, error), func(bool)) appservice.ClientUpdater {
	return nil
}
