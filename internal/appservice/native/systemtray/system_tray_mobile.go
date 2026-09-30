//go:build !server && (android || ios)

package systemtray

import (
	"github.com/runforyou-ai/cervi/internal/appservice"
)

// Controller 保持移动端与桌面端原生界面控制接口一致。
type Controller struct{}

// New 创建移动端原生界面控制器。
func New(_ appservice.Locale) *Controller {
	return &Controller{}
}

// Setup 保持移动端不启用桌面托盘能力。
func (*Controller) Setup(_ Options) {}

// SetLocale 保持移动端由前端和系统资源管理界面语言。
func (*Controller) SetLocale(_ appservice.Locale) {}
