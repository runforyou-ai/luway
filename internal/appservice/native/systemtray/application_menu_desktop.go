//go:build !server && !android && !ios

package systemtray

import (
	"runtime"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// localizedApplicationMenu 保存 macOS 原生应用菜单及其可本地化菜单项。
type localizedApplicationMenu struct {
	menu      *application.Menu
	roleItems map[application.Role]*application.MenuItem
}

var applicationMenuMessageKeys = map[application.Role]i18n.Key{
	application.AppMenu:            i18n.AppProductName,
	application.FileMenu:           i18n.AppMenuFile,
	application.EditMenu:           i18n.AppMenuEdit,
	application.ViewMenu:           i18n.AppMenuView,
	application.WindowMenu:         i18n.AppMenuWindow,
	application.About:              i18n.AppMenuAbout,
	application.ServicesMenu:       i18n.AppMenuServices,
	application.Hide:               i18n.AppMenuHide,
	application.HideOthers:         i18n.AppMenuHideOthers,
	application.UnHide:             i18n.AppMenuShowAll,
	application.Quit:               i18n.AppQuit,
	application.CloseWindow:        i18n.AppMenuClose,
	application.Undo:               i18n.AppMenuUndo,
	application.Redo:               i18n.AppMenuRedo,
	application.Cut:                i18n.AppMenuCut,
	application.Copy:               i18n.AppMenuCopy,
	application.Paste:              i18n.AppMenuPaste,
	application.PasteAndMatchStyle: i18n.AppMenuPasteAndMatchStyle,
	application.Delete:             i18n.AppMenuDelete,
	application.SelectAll:          i18n.AppMenuSelectAll,
	application.SpeechMenu:         i18n.AppMenuSpeech,
	application.StartSpeaking:      i18n.AppMenuStartSpeaking,
	application.StopSpeaking:       i18n.AppMenuStopSpeaking,
	application.Reload:             i18n.AppMenuReload,
	application.ForceReload:        i18n.AppMenuForceReload,
	application.ResetZoom:          i18n.AppMenuActualSize,
	application.ZoomIn:             i18n.AppMenuZoomIn,
	application.ZoomOut:            i18n.AppMenuZoomOut,
	application.ToggleFullscreen:   i18n.AppMenuToggleFullscreen,
	application.Minimise:           i18n.AppMenuMinimize,
	application.Zoom:               i18n.AppMenuZoom,
	application.Front:              i18n.AppMenuBringAllToFront,
}

// newApplicationMenu 创建 macOS 应用菜单，含应用、文件、编辑、显示和窗口菜单，不含开发者工具项。
func newApplicationMenu() *application.Menu {
	menu := application.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.FileMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.ViewMenu)
	menu.AddRole(application.WindowMenu)
	menu.RemoveMenuItem(menu.FindByRole(application.OpenDevTools))
	return menu
}

// newLocalizedApplicationMenu 按指定语言创建保留原生角色和快捷键的 macOS 应用菜单。
func newLocalizedApplicationMenu(app *application.App, locale appservice.Locale) *localizedApplicationMenu {
	if runtime.GOOS != "darwin" {
		return nil
	}

	menu := newApplicationMenu()
	controller := &localizedApplicationMenu{
		menu:      menu,
		roleItems: make(map[application.Role]*application.MenuItem, len(applicationMenuMessageKeys)),
	}
	for role := range applicationMenuMessageKeys {
		controller.roleItems[role] = menu.FindByRole(role)
	}
	controller.applyLocale(locale)
	app.Menu.Set(menu)
	return controller
}

// applyLocale 更新应用菜单模型中的全部本地化标签。
func (m *localizedApplicationMenu) applyLocale(locale appservice.Locale) {
	labels := i18n.LocalizeMap(string(locale), applicationMenuMessageKeys)
	for role, item := range m.roleItems {
		item.SetLabel(labels[role])
	}
}
