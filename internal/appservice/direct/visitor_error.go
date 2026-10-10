//go:build server

package direct

import (
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// visitorInternalError 返回按访客语言构造内部错误的函数。
func visitorInternalError(meta appservice.WebsiteVisitorMeta) func(error) *appservice.Error {
	return func(cause error) *appservice.Error {
		return appservice.WebsiteVisitorFailedError(meta.Locale, i18n.VisitorErrorInternal, cause)
	}
}
