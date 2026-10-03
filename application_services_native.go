//go:build !server

package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/runforyou-ai/luway/internal/apiproxy"
	"github.com/runforyou-ai/luway/internal/appservice"
	appservicenative "github.com/runforyou-ai/luway/internal/appservice/native"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// localComputer 把本机注册为电脑并执行派发给这台电脑的操作；不作为电脑的原生平台为空。
type localComputer interface {
	appservice.LocalComputerReporter
	// Start 开始注册与执行器连接。
	Start()
	// Stop 结束注册与执行器连接并等待其退出。
	Stop()
}

// applicationServices 创建原生端使用的远程应用服务和本机电脑；allowQuit 在应用内更新重启前放行应用退出。
func applicationServices(
	appStorage nativeStorage,
	nativeLocaleUpdater appservice.NativeLocaleUpdater,
	notification appservice.NativeNotification,
	serverLinks appservice.NativeServerLink,
	unreadIndicator appservice.UnreadIndicator,
	allowQuit func(bool),
) ([]application.Service, localComputer, error) {
	sessions, err := clientsession.NewManager(context.Background(), appStorage)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize client session: %w", err)
	}
	var backend *apiproxy.Backend
	backend, err = apiproxy.NewBackend(appStorage, defaultServerURL(), sessions, func(owner, name string, data any) {
		app := application.Get()
		// 无法识别发起窗口的事件投递给全部窗口，其余只投递给发起窗口。
		id, parseErr := strconv.ParseUint(owner, 10, 64)
		if parseErr != nil {
			app.Event.Emit(name, data)
			return
		}
		window, ok := app.Window.GetByID(uint(id))
		// 窗口关闭后才建立的事件流在首个事件时发现窗口已关闭，随即释放该窗口的登记。
		if !ok {
			backend.ReleaseWindow(owner)
			return
		}
		window.DispatchWailsEvent(&application.CustomEvent{Name: name, Data: data})
	}, func(ctx context.Context) string {
		// 绑定调用携带发起窗口，窗口刷新后重新连接据此关闭原有事件流。
		window, ok := ctx.Value(application.WindowKey).(application.Window)
		if !ok || window == nil {
			return ""
		}
		return strconv.FormatUint(uint64(window.ID()), 10)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize remote application backend: %w", err)
	}
	// 窗口关闭后结束该窗口的实时事件流并删除登记；已创建的窗口与之后创建的窗口都监听关闭。
	watchWindow := func(window application.Window) {
		owner := strconv.FormatUint(uint64(window.ID()), 10)
		window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { backend.ReleaseWindow(owner) })
	}
	for _, window := range application.Get().Window.GetAll() {
		watchWindow(window)
	}
	application.Get().Window.OnCreate(watchWindow)
	conversationWindows := appservicenative.NewConversationWindowOpener()
	// 会话独立窗口属于打开它的登录会话，登录、退出或切换服务器后一并关闭；移动端没有独立窗口。
	if conversationWindows != nil {
		sessions.Subscribe(conversationWindows.CloseAll)
	}
	options := []appservice.Option{
		appservice.WithImageSelector(appservicenative.NewImageSelector()),
		appservice.WithFileSaver(appservicenative.NewFileSaver()),
		appservice.WithNativeLocaleUpdater(nativeLocaleUpdater),
		appservice.WithNativeNotification(notification),
		appservice.WithNativeServerLink(serverLinks),
		appservice.WithUnreadIndicator(unreadIndicator),
		appservice.WithConversationWindowOpener(conversationWindows),
		appservice.WithClientUpdater(appservicenative.NewClientUpdater(backend, allowQuit)),
	}
	computer := newLocalComputer(appStorage, backend, sessions)
	if computer != nil {
		options = append(options, appservice.WithLocalComputer(computer))
		// 作为电脑的平台同时开放本机环境管理。
		if manager, ok := computer.(appservice.LocalEnvironmentManager); ok {
			options = append(options, appservice.WithLocalEnvironment(manager))
		}
	}
	service := appservice.New(backend, options...)
	return []application.Service{
		application.NewServiceWithOptions(service, application.ServiceOptions{
			MarshalError: appservice.MarshalError,
		}),
	}, computer, nil
}
