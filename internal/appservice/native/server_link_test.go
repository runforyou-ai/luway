//go:build !server

package native

import (
	"context"
	"net/url"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// TestOpenedServerLinkKeepsLatestServer 验证连接链接只接受本应用协议的连接地址，读取后清除，并在每次收到有效链接时调用登记的处理。
func TestOpenedServerLinkKeepsLatestServer(t *testing.T) {
	ctx := context.Background()
	scheme := brand.Build().Slug
	link := func(server string) string {
		return scheme + "://connect?server=" + url.QueryEscape(server)
	}
	var opened openedServerLink
	calls := 0
	opened.OnOpen(func() { calls++ })

	opened.Open(link("https://first.example.com"))
	opened.Open(link("https://second.example.com/team"))
	opened.Open("other://connect?server=https%3A%2F%2Fevil.example.com")
	opened.Open(scheme + "://open?server=https%3A%2F%2Fevil.example.com")
	opened.Open(scheme + "://connect")
	if serverURL, err := opened.TakeOpenedServerLink(ctx, appservice.RequestMeta{}); err != nil || serverURL != "https://second.example.com/team" {
		t.Fatalf("待连接的部署地址 = %q, err = %v", serverURL, err)
	}
	if serverURL, _ := opened.TakeOpenedServerLink(ctx, appservice.RequestMeta{}); serverURL != "" {
		t.Fatalf("读取后未清除部署地址 %q", serverURL)
	}
	if calls != 2 {
		t.Fatalf("链接处理次数 = %d", calls)
	}
}
