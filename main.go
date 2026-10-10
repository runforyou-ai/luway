//go:build !server

package main

import (
	"context"
	_ "embed"
	"log/slog"
	"os"
	"runtime"

	"github.com/runforyou-ai/luway/frontend"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/native"
	nativesystemlocale "github.com/runforyou-ai/luway/internal/native/systemlocale"
	nativesystemtray "github.com/runforyou-ai/luway/internal/native/systemtray"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed build/appicon.png
var nativeAppIcon []byte

//go:embed build/appicon.icon/Assets/icon.png
var nativeMacTrayTemplateIcon []byte

// main 启动应用并记录无法恢复的运行错误。
func main() {
	// 日志以文本格式写到标准错误输出并附带日志作用域，桌面端同时写入本机日志文件。
	slog.SetDefault(slog.New(logscope.Handler(slog.NewTextHandler(os.Stderr, nil))))
	closeLog := setupLocalLog()
	if err := run(); err != nil {
		slog.ErrorContext(context.Background(), "运行失败", "error", err)
		closeLog()
		os.Exit(1)
	}
	closeLog()
}

// run 创建窗口、托盘与本机能力服务，并运行 Wails 应用；业务数据由前端直接请求服务端。
func run() error {
	// 更新器重新启动的辅助进程在读取本机数据前完成程序替换并退出。
	updater.HandleHelperMode()
	systemLocale := nativesystemlocale.Detect()
	nativeAppName := brand.Build().DisplayName()
	if runtime.GOOS == "darwin" {
		nativeAppName = nativesystemtray.ProductName(systemLocale)
	}
	trayController := nativesystemtray.New(systemLocale)
	notificationProvider, notificationLifecycleServices := native.NewNotificationProvider()
	serverLinks, serverLinkServices := native.NewServerLinks()
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
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend.Assets),
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
		Frameless:        runtime.GOOS == "windows" || runtime.GOOS == "linux",
		Width:            1440,
		Height:           900,
		MinWidth:         1440,
		MinHeight:        900,
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              "/",
		Windows: application.WindowsWindow{
			NonClientRegionSupport:     true,
			WebView2CompositionHosting: true,
		},
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
	})
	computer := newLocalComputer()
	service := native.NewService(native.Options{
		Locale:        systemLocale,
		LocaleUpdater: trayController,
		Images:        native.NewImageSelector(),
		Files:         native.NewFileSaver(),
		Notifications: notificationProvider,
		ServerLinks:   serverLinks,
		Unread:        trayController,
		Windows:       native.NewConversationWindowOpener(),
		Updater:       native.NewClientUpdater(app),
		Computer:      computer,
	})
	app.RegisterService(application.NewServiceWithOptions(service, application.ServiceOptions{MarshalError: appservice.MarshalError}))
	if computer != nil {
		computer.Start()
		defer computer.Stop()
	}

	slog.InfoContext(context.Background(), "启动应用")
	return app.Run()
}
