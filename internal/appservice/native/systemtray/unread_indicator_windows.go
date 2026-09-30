//go:build !server && windows

package systemtray

import (
	"context"
	"sync"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

const windowsTrayFlashInterval = 500 * time.Millisecond

// windowsUnreadIndicator 根据未读状态控制 Windows 通知区域图标显隐闪烁。
type windowsUnreadIndicator struct {
	ctx     context.Context
	tray    *application.SystemTray
	changed chan struct{}

	mu          sync.Mutex
	shouldFlash bool
}

// newUnreadIndicator 创建 Windows 未读消息指示器并启动状态循环。
func newUnreadIndicator(app *application.App, tray *application.SystemTray, _ *dock.DockService) appservice.UnreadIndicator {
	indicator := &windowsUnreadIndicator{
		ctx:     app.Context(),
		tray:    tray,
		changed: make(chan struct{}, 1),
	}
	go indicator.run()
	return indicator
}

// SetUnreadState 根据提醒开关和待处理提醒状态启动或停止托盘图标闪烁。
func (i *windowsUnreadIndicator) SetUnreadState(state appservice.UnreadIndicatorState) error {
	i.mu.Lock()
	i.shouldFlash = state.Count > 0 && state.AttentionEnabled && state.AttentionPending
	i.mu.Unlock()

	select {
	case i.changed <- struct{}{}:
	default:
	}
	return nil
}

// run 根据最新提醒状态更新托盘图标。
func (i *windowsUnreadIndicator) run() {
	var ticker *time.Ticker
	var ticks <-chan time.Time
	visible := true

	stopFlashing := func() {
		if ticker != nil {
			ticker.Stop()
			ticker = nil
			ticks = nil
		}
		visible = true
		i.tray.Show()
	}

	for {
		select {
		case <-i.changed:
			i.mu.Lock()
			shouldFlash := i.shouldFlash
			i.mu.Unlock()

			if !shouldFlash {
				stopFlashing()
				continue
			}
			if ticker == nil {
				ticker = time.NewTicker(windowsTrayFlashInterval)
				ticks = ticker.C
				visible = false
				i.tray.Hide()
			}
		case <-ticks:
			visible = !visible
			if visible {
				i.tray.Show()
			} else {
				i.tray.Hide()
			}
		case <-i.ctx.Done():
			if ticker != nil {
				ticker.Stop()
			}
			return
		}
	}
}
