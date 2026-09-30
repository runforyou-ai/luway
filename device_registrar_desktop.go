//go:build !server && !ios && !android

package main

import (
	"log/slog"

	"github.com/runforyou-ai/luway/internal/apiproxy"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/runforyou-ai/luway/internal/devicehost"
	"github.com/runforyou-ai/luway/internal/storage"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// nativeStorage 组合桌面端连接、登录凭据与设备注册存储能力。
type nativeStorage interface {
	apiproxy.Store
	clientsession.Store
	devicehost.Store
}

// newDeviceRegistrar 创建桌面端本机设备，本机界面查看执行中的运行时直接读取本机过程流；无法注册设备时返回 nil。
func newDeviceRegistrar(appStorage nativeStorage, backend *apiproxy.Backend, sessions *clientsession.Manager) deviceRegistrar {
	dataDirectory, err := storage.DesktopDataDirectory()
	if err != nil {
		slog.Error("无法确定桌面端数据目录，本机设备不注册", "error", err)
		return nil
	}
	device := devicehost.NewDesktop(appStorage, backend, sessions, devicehost.DesktopOptions{
		DataDir: dataDirectory,
		// 本机环境或各工作区的本机注册结果变化时通知界面重新读取本机设备与本机环境。
		Notify:     func() { application.Get().Event.Emit(appservice.LocalDeviceChangedEventName) },
		OpenFolder: func(dir string) error { return application.Get().Env.OpenFileManager(dir, false) },
	})
	if device == nil {
		return nil
	}
	backend.UseLocalRunStreams(device.Worker())
	return device
}
