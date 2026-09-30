//go:build !server

package systemtray

import (
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Options 定义原生系统托盘初始化所需的应用资源与回调。
type Options struct {
	App             *application.App
	Window          application.Window
	Icon            []byte
	MacTemplateIcon []byte
	RequestQuit     func()
}

// localizedTexts 定义原生窗口和托盘使用的本地化文案。
type localizedTexts struct {
	ProductName string
	Open        string
	Quit        string
}

// ProductName 返回当前语言对应的原生界面产品名。
func ProductName(locale appservice.Locale) string {
	return textsForLocale(locale).ProductName
}

// textsForLocale 返回原生界面使用的本地化文案。
func textsForLocale(locale appservice.Locale) localizedTexts {
	messages := i18n.LocalizeMap(string(locale), map[string]i18n.Key{
		"productName": i18n.AppProductName,
		"open":        i18n.AppTrayOpen,
		"quit":        i18n.AppQuit,
	})
	return localizedTexts{
		ProductName: messages["productName"],
		Open:        messages["open"],
		Quit:        messages["quit"],
	}
}
