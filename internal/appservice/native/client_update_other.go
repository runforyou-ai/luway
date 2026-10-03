//go:build !server && (ios || android)

package native

import (
	"context"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewClientUpdater 在移动端禁用从服务器更新客户端，移动端经应用商店更新。
func NewClientUpdater(*application.App, func(context.Context) (string, error), func(bool)) appservice.ClientUpdater {
	return nil
}
