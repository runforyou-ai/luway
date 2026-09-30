//go:build !server

package native

import "github.com/runforyou-ai/luway/internal/appservice"

// ConversationWindows 打开会话独立窗口，并在登录会话变化时关闭全部已开窗口。
type ConversationWindows interface {
	appservice.ConversationWindowOpener
	// CloseAll 关闭全部会话独立窗口。
	CloseAll()
}
