//go:build !server && !ios && !android

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/locallog"
	"github.com/runforyou-ai/luway/internal/computerhost"
	"github.com/runforyou-ai/luway/internal/native"
	desktopstorage "github.com/runforyou-ai/luway/internal/storage/desktop"
	"github.com/runforyou-ai/support/random"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// localComputer 是作为电脑的本机：保存前端注册得到的电脑凭据并执行派发给这台电脑的操作，Start 与 Stop 启停执行器连接。
type localComputer interface {
	native.ComputerHost
	Start()
	Stop()
}

// setupLocalLog 把桌面端日志同时写到数据目录 logs 下的 desktop.log，返回关闭日志文件的函数；无法打开文件时日志只写到标准错误输出。
func setupLocalLog() func() {
	dataDirectory, err := desktopstorage.DataDirectory()
	if err == nil {
		var closeFile func() error
		if closeFile, err = locallog.Setup(filepath.Join(dataDirectory, "logs", "desktop.log")); err == nil {
			return func() { _ = closeFile() }
		}
	}
	slog.WarnContext(context.Background(), "无法打开桌面端日志文件，日志只写到标准错误输出", "error", err)
	return func() {}
}

// newLocalComputer 创建桌面端电脑；无法作为电脑时返回 nil。
func newLocalComputer() localComputer {
	dataDirectory, err := desktopstorage.DataDirectory()
	if err != nil {
		slog.ErrorContext(context.Background(), "无法确定桌面端数据目录，这台电脑不注册", "error", err)
		return nil
	}
	computer := computerhost.NewDesktop(computerhost.DesktopOptions{
		DataDir: dataDirectory,
		// 本机环境或注册结果变化时通知界面重新读取本机电脑与本机环境。
		Notify:     func() { application.Get().Event.Emit(native.LocalComputerChangedEventName) },
		OpenFolder: func(dir string) error { return application.Get().Env.OpenFileManager(dir, false) },
	})
	// 空指针转为空接口，调用方据此判断这台电脑不注册。
	if computer == nil {
		return nil
	}
	return computer
}

// singleInstanceOptions 返回桌面端单实例配置：Windows 与 Linux 上同一数据目录只运行一个实例，再次启动时把主窗口带到前台，并把系统交来的连接链接转给已运行的实例；macOS 由系统把链接交给已运行的应用。
func singleInstanceOptions(serverLinks native.ServerLinks, showMainWindow func()) *application.SingleInstanceOptions {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return nil
	}
	dataDirectory, err := desktopstorage.DataDirectory()
	if err != nil {
		slog.WarnContext(context.Background(), "读取桌面端数据目录失败，不限制单实例", "error", err)
		return nil
	}
	// 实例标识按数据目录区分，使用独立数据目录的开发实例可以同时运行。
	return &application.SingleInstanceOptions{
		UniqueID: brand.Build().Identifier + ".d" + random.SHA256Hex([]byte(dataDirectory))[:12],
		OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			showMainWindow()
			for _, arg := range data.Args {
				if strings.Contains(arg, "://") {
					serverLinks.Open(arg)
				}
			}
		},
	}
}
