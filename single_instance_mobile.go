//go:build !server && (ios || android)

package main

import (
	appservicenative "github.com/runforyou-ai/cervi/internal/appservice/native"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// singleInstanceOptions 返回移动端单实例配置；移动端由系统保证单实例并交来链接。
func singleInstanceOptions(appservicenative.ServerLinks, func()) *application.SingleInstanceOptions {
	return nil
}
