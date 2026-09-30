//go:build server

package direct

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

// TestWebsiteVisitorContext 验证页面地址只保留源与路径，并按请求头解析设备与国家代码。
func TestWebsiteVisitorContext(t *testing.T) {
	if websiteVisitorContext(appservice.WebsiteVisitorMeta{}, nil) != nil {
		t.Fatal("未上报页面时应不更新访客上下文")
	}
	visit := websiteVisitorContext(appservice.WebsiteVisitorMeta{
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		Country:   " us ",
	}, &appservice.WebsiteVisitorPage{URL: "https://user:pass@shop.example.com/reset?token=abc#top", Referrer: "javascript:alert(1)", Title: " 重置密码 "})
	if visit.PageURL != "https://shop.example.com/reset" || visit.ReferrerURL != "" || visit.PageTitle != "重置密码" || visit.Country != "US" || visit.DeviceType != "mobile" || visit.OS == "" {
		t.Fatalf("visit=%+v", visit)
	}
}
