//go:build darwin && !server && !ios

package systemtray

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestApplicationMenu 校验应用菜单含全部参与本地化的角色，且不含开发者工具项和帮助菜单。
func TestApplicationMenu(t *testing.T) {
	application.New(application.Options{Name: "App"})
	menu := newApplicationMenu()
	for role := range applicationMenuMessageKeys {
		if menu.FindByRole(role) == nil {
			t.Errorf("菜单缺少参与本地化的角色 %v", role)
		}
	}
	if menu.FindByRole(application.OpenDevTools) != nil {
		t.Error("菜单包含开发者工具项")
	}
	if menu.FindByRole(application.HelpMenu) != nil {
		t.Error("菜单包含帮助菜单")
	}
}
