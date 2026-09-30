//go:build !server && !android && !ios

package systemtray

import (
	"log/slog"
	"runtime"
	"sync"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

// Controller 同步桌面端窗口、应用菜单、托盘语言和未读消息提示。
type Controller struct {
	mu              sync.Mutex
	localeUpdateMu  sync.Mutex
	locale          appservice.Locale
	window          application.Window
	applicationMenu *localizedApplicationMenu
	tray            *application.SystemTray
	openItem        *application.MenuItem
	quitItem        *application.MenuItem
	unread          appservice.UnreadIndicator
}

// New 创建使用系统语言作为未登录默认值的托盘控制器。
func New(systemLocale appservice.Locale) *Controller {
	return &Controller{locale: systemLocale}
}

// Setup 配置桌面端托盘、托盘菜单和关闭窗口时隐藏到托盘的行为。
func (c *Controller) Setup(options Options) {
	showWindow := func() {
		if options.Window.IsMinimised() {
			options.Window.UnMinimise()
		}
		options.Window.Show().Focus()
	}

	// 读取控制器当前使用的语言。
	c.mu.Lock()
	locale := c.locale
	c.mu.Unlock()
	texts := textsForLocale(locale)
	applicationMenu := newLocalizedApplicationMenu(options.App, locale)
	trayMenu := options.App.Menu.New()
	openItem := trayMenu.Add(texts.Open).OnClick(func(_ *application.Context) {
		showWindow()
	})
	quitItem := trayMenu.Add(texts.Quit).OnClick(func(_ *application.Context) {
		options.RequestQuit()
		options.App.Quit()
	})

	tray := options.App.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(options.MacTemplateIcon)
	} else {
		tray.SetIcon(options.Icon)
	}
	tray.SetTooltip(texts.ProductName)
	tray.SetMenu(trayMenu)
	tray.OnClick(showWindow)
	tray.OnRightClick(tray.OpenMenu)

	options.Window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		options.Window.Hide()
	})

	dockService := dock.New()
	options.App.RegisterService(application.NewService(dockService))

	c.mu.Lock()
	c.window = options.Window
	c.applicationMenu = applicationMenu
	c.tray = tray
	c.openItem = openItem
	c.quitItem = quitItem
	c.unread = newUnreadIndicator(options.App, tray, dockService)
	c.mu.Unlock()
	options.Window.SetTitle(texts.ProductName)
}

// SetLocale 按当前用户偏好更新窗口、应用菜单和托盘文案。
func (c *Controller) SetLocale(locale appservice.Locale) {
	c.localeUpdateMu.Lock()
	defer c.localeUpdateMu.Unlock()

	texts := textsForLocale(locale)
	c.mu.Lock()
	c.locale = locale
	window := c.window
	applicationMenu := c.applicationMenu
	tray := c.tray
	openItem := c.openItem
	quitItem := c.quitItem
	c.mu.Unlock()

	if window != nil {
		window.SetTitle(texts.ProductName)
	}
	if applicationMenu != nil {
		// 更新 macOS 原生应用菜单并立即刷新屏幕顶部菜单栏。
		applicationMenu.applyLocale(locale)
		applicationMenu.menu.Update()
	}
	if tray != nil {
		tray.SetTooltip(texts.ProductName)
	}
	if openItem != nil || quitItem != nil {
		application.InvokeSync(func() {
			if openItem != nil {
				openItem.SetLabel(texts.Open)
			}
			if quitItem != nil {
				quitItem.SetLabel(texts.Quit)
			}
		})
	}
	slog.Info("已更新桌面端原生界面语言", "locale", locale)
}

// SetUnreadState 更新当前平台的原生未读消息提示。
func (c *Controller) SetUnreadState(state appservice.UnreadIndicatorState) error {
	c.mu.Lock()
	unread := c.unread
	c.mu.Unlock()
	if unread == nil {
		return nil
	}
	return unread.SetUnreadState(state)
}
