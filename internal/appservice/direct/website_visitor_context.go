//go:build server

package direct

import (
	"net/url"
	"strings"

	"github.com/mileusna/useragent"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
)

// maxVisitorContextText 是访客上下文单个文本字段保留的最大字符数。
const maxVisitorContextText = 512

// websiteVisitorContext 按访客上报的宿主页面与请求头构造访客上下文，未上报页面时返回空；页面地址只保留源与路径。
func websiteVisitorContext(meta appservice.WebsiteVisitorMeta, page *appservice.WebsiteVisitorPage) *domain.VisitorContext {
	if page == nil {
		return nil
	}
	visitorContext := &domain.VisitorContext{
		ReferrerURL: visitorPageURL(page.Referrer),
		PageURL:     visitorPageURL(page.URL),
		PageTitle:   visitorContextText(page.Title),
		Language:    visitorContextText(page.Language),
		TimeZone:    visitorContextText(page.TimeZone),
		Country:     strings.ToUpper(visitorContextText(meta.Country)),
	}
	if meta.UserAgent != "" {
		agent := useragent.Parse(meta.UserAgent)
		visitorContext.Browser = visitorContextText(strings.TrimSpace(agent.Name + " " + agent.Version))
		visitorContext.OS = visitorContextText(strings.TrimSpace(agent.OS + " " + agent.OSVersion))
		switch {
		case agent.Tablet:
			visitorContext.DeviceType = string(domain.VisitorDeviceTablet)
		case agent.Mobile:
			visitorContext.DeviceType = string(domain.VisitorDeviceMobile)
		case agent.Desktop:
			visitorContext.DeviceType = string(domain.VisitorDeviceDesktop)
		}
	}
	return visitorContext
}

// visitorPageURL 只保留 http 与 https 地址的源与路径，去掉查询串、hash 与用户信息，其他地址返回空。
func visitorPageURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	cleaned := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}
	return visitorContextText(cleaned.String())
}

// visitorContextText 去除首尾空白并按字符数截断。
func visitorContextText(value string) string {
	return str.Substr(strings.TrimSpace(value), 0, maxVisitorContextText)
}
