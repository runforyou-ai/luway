//go:build !server && android

package native

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// serverLinkEvent 是 Android 桥接转来唤起应用的链接使用的事件名。
const serverLinkEvent = "app:launch-link"

// serverLinkLifecycle 在应用启动时订阅 Android 桥接转来的唤起链接。
type serverLinkLifecycle struct {
	links       *openedServerLink
	unsubscribe func()
}

// NewServerLinks 创建 Android 连接链接能力及其 Wails 生命周期服务。
func NewServerLinks() (ServerLinks, []application.Service) {
	links := &openedServerLink{}
	return links, []application.Service{application.NewService(&serverLinkLifecycle{links: links})}
}

// ServiceStartup 订阅桥接转来的唤起链接，并告知桥接可以转来链接；应用被链接唤起时，桥接暂存的链接随之转来。
func (l *serverLinkLifecycle) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	l.unsubscribe = application.Get().Event.On(serverLinkEvent, func(event *application.CustomEvent) {
		data, _ := event.Data.(map[string]any)
		rawURL, _ := data["url"].(string)
		l.links.Open(rawURL)
	})
	payload, err := json.Marshal(map[string]string{"action": "listen-launch-link"})
	if err != nil {
		return err
	}
	application.Android.Notify(string(payload))
	slog.Info("开始接收移动端唤起链接")
	return nil
}

// ServiceShutdown 取消订阅桥接转来的唤起链接。
func (l *serverLinkLifecycle) ServiceShutdown() error {
	if l.unsubscribe != nil {
		l.unsubscribe()
		l.unsubscribe = nil
	}
	return nil
}
