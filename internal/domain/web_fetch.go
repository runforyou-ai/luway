package domain

// WebFetchProvider 定义网页读取服务商。
type WebFetchProvider string

const (
	// WebFetchProviderDirect 表示服务端直连抓取，不执行 JavaScript。
	WebFetchProviderDirect WebFetchProvider = "direct"
	// WebFetchProviderJina 表示 Jina Reader。
	WebFetchProviderJina WebFetchProvider = "jina"
	// WebFetchProviderFirecrawl 表示 Firecrawl Scrape。
	WebFetchProviderFirecrawl WebFetchProvider = "firecrawl"
)

// ValidWebFetchProvider 判断网页读取服务商是否为已支持取值。
func ValidWebFetchProvider(provider WebFetchProvider) bool {
	switch provider {
	case WebFetchProviderDirect, WebFetchProviderJina, WebFetchProviderFirecrawl:
		return true
	}
	return false
}

// WebFetchProviderNeedsCredential 判断服务商是否需要 API Key。
func WebFetchProviderNeedsCredential(provider WebFetchProvider) bool {
	return provider == WebFetchProviderJina || provider == WebFetchProviderFirecrawl
}
