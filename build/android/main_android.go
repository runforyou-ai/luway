//go:build android

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// init 注册 Android 应用初始化时执行的 Go 入口。
func init() {
	application.RegisterAndroidMain(main)
}
