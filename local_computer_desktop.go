//go:build !server && !ios && !android

package main

import (
	"log/slog"

	"github.com/runforyou-ai/luway/internal/apiproxy"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/runforyou-ai/luway/internal/computerhost"
	"github.com/runforyou-ai/luway/internal/storage"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// nativeStorage 组合桌面端连接、登录凭据与电脑注册存储能力。
type nativeStorage interface {
	apiproxy.Store
	clientsession.Store
	computerhost.Store
}

// newLocalComputer 创建桌面端电脑：注册到登录账号的各工作区并以电脑凭据执行派发给这台电脑的操作；无法作为电脑时返回 nil。
func newLocalComputer(appStorage nativeStorage, backend *apiproxy.Backend, sessions *clientsession.Manager) localComputer {
	dataDirectory, err := storage.DesktopDataDirectory()
	if err != nil {
		slog.Error("无法确定桌面端数据目录，这台电脑不注册", "error", err)
		return nil
	}
	computer := computerhost.NewDesktop(appStorage, backend, sessions, computerhost.DesktopOptions{
		DataDir: dataDirectory,
		// 本机环境或各工作区的本机注册结果变化时通知界面重新读取本机电脑与本机环境。
		Notify:     func() { application.Get().Event.Emit(appservice.LocalComputerChangedEventName) },
		OpenFolder: func(dir string) error { return application.Get().Env.OpenFileManager(dir, false) },
	})
	if computer == nil {
		return nil
	}
	return computer
}
