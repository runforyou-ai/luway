//go:build !server

package native

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Notifications 是原生端的系统通知能力；OnOpen 登记通知被点击后把主窗口带到前台的处理。
type Notifications interface {
	appservice.NativeNotification
	OnOpen(func())
}

// openedNotification 记录最近一次被点击的通知要打开的页面地址，直到主界面取走；应用刚被点击唤起、界面尚未就绪时由界面启动后读取。
type openedNotification struct {
	mu     sync.Mutex
	path   string
	onOpen func()
}

// OnOpen 登记通知被点击后的处理。
func (o *openedNotification) OnOpen(handler func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onOpen = handler
}

// open 记录被点击的通知要打开的页面地址并通知主界面；只接受工作区内的页面地址，其他取值清除待打开的页面，只把应用带到前台。
func (o *openedNotification) open(path string) {
	o.mu.Lock()
	o.path = ""
	if strings.HasPrefix(path, "/w/") {
		o.path = path
	}
	handler := o.onOpen
	o.mu.Unlock()
	slog.Info("系统通知被点击", "has_path", path != "")
	if handler != nil {
		handler()
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(appservice.NotificationOpenedEventName)
	}
}

// TakeOpenedNotificationPath 返回并清除待打开的页面地址。
func (o *openedNotification) TakeOpenedNotificationPath(context.Context, appservice.RequestMeta) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	path := o.path
	o.path = ""
	return path, nil
}
