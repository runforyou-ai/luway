//go:build !server && linux && !android

package systemtray

import (
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

// noopUnreadIndicator 忽略 Linux 原生未读状态。
type noopUnreadIndicator struct{}

// SetUnreadState 忽略未读状态。
func (*noopUnreadIndicator) SetUnreadState(_ appservice.UnreadIndicatorState) error {
	return nil
}

// newUnreadIndicator 禁用 Linux 原生未读提示。
func newUnreadIndicator(_ *application.App, _ *application.SystemTray, _ *dock.DockService) appservice.UnreadIndicator {
	return &noopUnreadIndicator{}
}
