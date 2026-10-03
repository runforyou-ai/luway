//go:build !server

package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"

	appservicenative "github.com/runforyou-ai/luway/internal/appservice/native"
	nativesystemlocale "github.com/runforyou-ai/luway/internal/appservice/native/systemlocale"
	nativesystemtray "github.com/runforyou-ai/luway/internal/appservice/native/systemtray"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/storage"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed build/appicon.png
var nativeAppIcon []byte

//go:embed build/appicon.icon/Assets/icon.png
var nativeMacTrayTemplateIcon []byte

// run 初始化原生端存储与应用服务，并运行 Wails 应用。
func run(_ []string) error {
	// 更新器重新启动的辅助进程在打开本机存储前完成程序替换并退出。
	updater.HandleHelperMode()
	appStorage, err := storage.Open(context.Background())
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer func() {
		if err := appStorage.Close(); err != nil {
			slog.Warn("关闭存储失败", "error", err)
		}
	}()
	systemLocale := nativesystemlocale.Detect()
	nativeAppName := brand.Build().DisplayName()
	if runtime.GOOS == "darwin" {
		nativeAppName = nativesystemtray.ProductName(systemLocale)
	}
	trayController := nativesystemtray.New(systemLocale)
	notificationProvider, notificationLifecycleServices := appservicenative.NewNotificationProvider()
	serverLinks, serverLinkServices := appservicenative.NewServerLinks()
	var trayQuitRequested atomic.Bool
	var mainWindow *application.WebviewWindow
	// showMainWindow 把主窗口带到前台。
	showMainWindow := func() {
		if mainWindow != nil {
			mainWindow.Show()
			mainWindow.Focus()
		}
	}
	app := application.New(application.Options{
		Name:                        nativeAppName,
		Description:                 brand.Build().Description,
		Services:                    append(notificationLifecycleServices, serverLinkServices...),
		SingleInstance:              singleInstanceOptions(serverLinks, showMainWindow),
		DisableDefaultSignalHandler: runtime.GOOS == "ios",
		ShouldQuit: func() bool {
			if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
				return true
			}
			return trayQuitRequested.Load()
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Server: application.ServerOptions{
			Port: 8080,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		IOS: application.IOSOptions{
			DisableBounce:           true,
			DisableScrollIndicators: true,
		},
	})

	// 主窗口隐藏创建，前端按入口页或工作台调整尺寸后显示。
	mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            nativesystemtray.ProductName(systemLocale),
		Hidden:           true,
		Width:            1440,
		Height:           900,
		MinWidth:         1440,
		MinHeight:        900,
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              "/",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHidden,
		},
	})
	// 点击系统通知时把主窗口带到前台，主界面随后打开通知携带的页面。
	notificationProvider.OnOpen(showMainWindow)
	// 收到连接链接时把主窗口带到前台，主界面随后进入连接页。
	serverLinks.OnOpen(showMainWindow)
	// 桌面端由系统打开连接链接时交给连接链接能力。
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(event *application.ApplicationEvent) {
		serverLinks.Open(event.Context().URL())
	})
	trayController.Setup(nativesystemtray.Options{
		App:             app,
		Window:          mainWindow,
		Icon:            nativeAppIcon,
		MacTemplateIcon: nativeMacTrayTemplateIcon,
		RequestQuit: func() {
			trayQuitRequested.Store(true)
		},
	})
	services, registrar, err := applicationServices(
		appStorage,
		trayController,
		notificationProvider,
		serverLinks,
		trayController,
		trayQuitRequested.Store,
	)
	if err != nil {
		return fmt.Errorf("initialize application services: %w", err)
	}
	for _, service := range services {
		app.RegisterService(service)
	}
	if registrar != nil {
		registrar.Start()
		defer registrar.Stop()
	}

	// 桌面开发时同时打开移动端预览窗口，共用当前原生服务。
	if app.Env.Info().Debug && runtime.GOOS != "ios" && runtime.GOOS != "android" {
		app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "mobile-preview",
			Title:            nativesystemtray.ProductName(systemLocale) + " · 移动端预览",
			Width:            390,
			Height:           844,
			DisableResize:    true,
			BackgroundColour: application.NewRGB(250, 250, 250),
			URL:              "/?preview=mobile",
		})
		slog.Info("已创建移动端预览窗口")
	}

	slog.Info("启动应用")
	return app.Run()
}
