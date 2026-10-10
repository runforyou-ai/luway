//go:build ios

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// modifyOptionsForIOS 配置 iOS 平台所需的应用选项。
func modifyOptionsForIOS(opts *application.Options) {
	opts.DisableDefaultSignalHandler = true
}
