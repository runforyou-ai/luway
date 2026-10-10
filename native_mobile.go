//go:build !server && (ios || android)

package main

import (
	"github.com/runforyou-ai/luway/internal/native"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// localComputer 是作为电脑的本机，移动端不作为电脑。
type localComputer interface {
	native.ComputerHost
	Start()
	Stop()
}

// setupLocalLog 保持日志只写到标准错误输出，移动端不写本机日志文件。
func setupLocalLog() func() {
	return func() {}
}

// newLocalComputer 返回空电脑，移动端不作为电脑执行操作。
func newLocalComputer() localComputer {
	return nil
}

// singleInstanceOptions 返回移动端单实例配置；移动端由系统保证单实例并交来链接。
func singleInstanceOptions(native.ServerLinks, func()) *application.SingleInstanceOptions {
	return nil
}
