//go:build !server

package native

import (
	"context"
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

// TestOpenedNotificationKeepsLatestWorkspacePath 验证被点击的通知只记录工作区内的页面地址，其他地址清除待打开的页面，读取后清除，并在每次点击时调用登记的处理。
func TestOpenedNotificationKeepsLatestWorkspacePath(t *testing.T) {
	ctx := context.Background()
	var opened openedNotification
	calls := 0
	opened.OnOpen(func() { calls++ })

	opened.open("/w/first/chats?conversation=1")
	opened.open("/w/second/inbox?conversation=2")
	if path, err := opened.TakeOpenedNotificationPath(ctx, appservice.RequestMeta{}); err != nil || path != "/w/second/inbox?conversation=2" {
		t.Fatalf("待打开的页面 = %q, err = %v", path, err)
	}
	opened.open("/w/third/chats?conversation=3")
	opened.open("https://evil.example.com")
	if path, _ := opened.TakeOpenedNotificationPath(ctx, appservice.RequestMeta{}); path != "" {
		t.Fatalf("非法地址未清除待打开的页面 %q", path)
	}
	if calls != 4 {
		t.Fatalf("点击处理次数 = %d", calls)
	}
}
