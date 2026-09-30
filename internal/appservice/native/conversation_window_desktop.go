//go:build !server && ((darwin && !ios) || windows || (linux && !android))

package native

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// conversationWindowOpener 在桌面端用独立窗口打开会话，并按会话编号复用已开窗口。
type conversationWindowOpener struct {
	mu      sync.Mutex
	windows map[string]*application.WebviewWindow
}

// NewConversationWindowOpener 创建桌面端会话独立窗口能力。
func NewConversationWindowOpener() ConversationWindows {
	return &conversationWindowOpener{windows: make(map[string]*application.WebviewWindow)}
}

// OpenConversationWindow 在会话所在工作区的地址下打开会话独立窗口；同一会话已打开时聚焦现有窗口。
func (o *conversationWindowOpener) OpenConversationWindow(_ context.Context, _ appservice.RequestMeta, input appservice.ConversationWindowInput) error {
	if !domain.WorkspaceSlugValid(input.WorkspaceSlug) {
		return errors.New("workspace slug is invalid")
	}
	app := application.Get()
	if app == nil {
		return errors.New("application not running")
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if window, ok := o.windows[input.ConversationID]; ok {
		window.Restore()
		window.Focus()
		slog.Info("已聚焦会话独立窗口", "conversation_id", input.ConversationID)
		return nil
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            input.Title,
		Width:            960,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(250, 250, 250),
		URL:              "/#/w/" + input.WorkspaceSlug + "/conversations/" + url.PathEscape(input.ConversationID),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHidden,
		},
	})
	o.windows[input.ConversationID] = window
	window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		o.mu.Lock()
		delete(o.windows, input.ConversationID)
		o.mu.Unlock()
	})
	slog.Info("已打开会话独立窗口", "conversation_id", input.ConversationID)
	return nil
}

// CloseAll 关闭全部会话独立窗口；登录会话变化（登录、退出、切换服务器）后原窗口属于上一会话。
func (o *conversationWindowOpener) CloseAll() {
	o.mu.Lock()
	windows := o.windows
	o.windows = make(map[string]*application.WebviewWindow)
	o.mu.Unlock()
	for _, window := range windows {
		window.Close()
	}
	if len(windows) > 0 {
		slog.Info("登录会话已变化，关闭会话独立窗口", "count", len(windows))
	}
}
