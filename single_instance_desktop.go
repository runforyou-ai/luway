//go:build !server && !ios && !android

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"runtime"
	"strings"

	appservicenative "github.com/runforyou-ai/luway/internal/appservice/native"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/storage"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// singleInstanceOptions 返回桌面端单实例配置：Windows 与 Linux 上同一数据目录只运行一个实例，再次启动时把主窗口带到前台，并把系统交来的连接链接转给已运行的实例；macOS 由系统把链接交给已运行的应用。
func singleInstanceOptions(serverLinks appservicenative.ServerLinks, showMainWindow func()) *application.SingleInstanceOptions {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return nil
	}
	dataDirectory, err := storage.DesktopDataDirectory()
	if err != nil {
		slog.Warn("读取桌面端数据目录失败，不限制单实例", "error", err)
		return nil
	}
	// 实例标识按数据目录区分，使用独立数据目录的开发实例可以同时运行。
	digest := sha256.Sum256([]byte(dataDirectory))
	return &application.SingleInstanceOptions{
		UniqueID: brand.Build().Identifier + ".d" + hex.EncodeToString(digest[:6]),
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
