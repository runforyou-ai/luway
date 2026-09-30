//go:build !server

package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/runforyou-ai/cervi/internal/apiproxy"
	"github.com/runforyou-ai/cervi/internal/appservice"
	appservicenative "github.com/runforyou-ai/cervi/internal/appservice/native"
	"github.com/runforyou-ai/cervi/internal/clientsession"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// deviceRegistrar 把本机注册为企业设备并执行派发给本机的运行；不注册设备的原生平台为空。
type deviceRegistrar interface {
	appservice.LocalDeviceReporter
	// Start 开始注册与执行循环。
	Start()
	// Stop 结束注册与执行循环并等待其退出。
	Stop()
}

// applicationServices 创建原生端使用的远程应用服务和本机设备注册器。
func applicationServices(
	appStorage nativeStorage,
	nativeLocaleUpdater appservice.NativeLocaleUpdater,
	notification appservice.NativeNotification,
	serverLinks appservice.NativeServerLink,
	unreadIndicator appservice.UnreadIndicator,
) ([]application.Service, deviceRegistrar, error) {
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
		appservice.WithNativeLocaleUpdater(nativeLocaleUpdater),
		appservice.WithNativeNotification(notification),
		appservice.WithNativeServerLink(serverLinks),
		appservice.WithUnreadIndicator(unreadIndicator),
		appservice.WithConversationWindowOpener(conversationWindows),
	}
	registrar := newDeviceRegistrar(appStorage, backend, sessions)
	if registrar != nil {
		options = append(options, appservice.WithLocalDevice(registrar))
		// 为助理提供本机运行环境的平台同时开放本机环境管理。
		if manager, ok := registrar.(appservice.LocalEnvironmentManager); ok {
			options = append(options, appservice.WithLocalEnvironment(manager))
		}
	}
	service := appservice.New(backend, options...)
	return []application.Service{
		application.NewServiceWithOptions(service, application.ServiceOptions{
			MarshalError: appservice.MarshalError,
		}),
	}, registrar, nil
}
