//go:build !server && darwin && !ios

package systemtray

import (
	"github.com/runforyou-ai/luway/internal/native"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

// darwinUnreadIndicator 同步更新 macOS 菜单栏托盘文字和 Dock 角标。
type darwinUnreadIndicator struct {
	tray *application.SystemTray
	dock *dock.DockService
}

// newUnreadIndicator 创建 macOS 未读消息指示器。
func newUnreadIndicator(_ *application.App, tray *application.SystemTray, dockService *dock.DockService) native.UnreadIndicator {
	return &darwinUnreadIndicator{tray: tray, dock: dockService}
}

// SetUnreadState 更新菜单栏托盘文字和 Dock 角标。
func (i *darwinUnreadIndicator) SetUnreadState(state native.UnreadIndicatorState) error {
	label := native.FormatUnreadCount(state.Count)
	i.tray.SetLabel(label)
	if label == "" {
		return i.dock.RemoveBadge()
	}
	return i.dock.SetBadge(label)
}
