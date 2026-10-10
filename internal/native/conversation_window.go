//go:build !server

package native

import (
	"context"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// ConversationWindows 打开会话独立窗口，并在登录会话变化时关闭全部已开窗口。
type ConversationWindows interface {
	// OpenConversationWindow 在独立窗口打开指定会话，同一会话已打开时聚焦现有窗口。
	OpenConversationWindow(context.Context, appservice.RequestMeta, ConversationWindowInput) error
	// CloseAll 关闭全部会话独立窗口。
	CloseAll()
}
