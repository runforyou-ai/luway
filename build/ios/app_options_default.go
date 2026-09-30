//go:build !ios

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// modifyOptionsForIOS 保持非 iOS 平台的应用配置不变。
func modifyOptionsForIOS(opts *application.Options) {
}
