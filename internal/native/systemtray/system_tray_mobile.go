//go:build !server && (android || ios)

package systemtray

import (
	"github.com/runforyou-ai/luway/internal/appservice"
)

// Controller 是移动端原生界面控制器，不提供托盘与应用菜单。
type Controller struct{}

// New 创建移动端原生界面控制器。
func New(_ appservice.Locale) *Controller {
	return &Controller{}
}

// Setup 在移动端不执行操作。
func (*Controller) Setup(_ Options) {}

// SetLocale 在移动端不执行操作，界面语言由前端和系统资源管理。
func (*Controller) SetLocale(_ appservice.Locale) {}
