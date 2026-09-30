package domain

// WebSearchProvider 定义联网搜索服务商。
type WebSearchProvider string

const (
	WebSearchProviderTavily     WebSearchProvider = "tavily"
	WebSearchProviderBrave      WebSearchProvider = "brave"
	WebSearchProviderExa        WebSearchProvider = "exa"
	WebSearchProviderPerplexity WebSearchProvider = "perplexity"
	WebSearchProviderSerper     WebSearchProvider = "serper"
	WebSearchProviderSerpAPI    WebSearchProvider = "serpapi"
	WebSearchProviderJina       WebSearchProvider = "jina"
	WebSearchProviderFirecrawl  WebSearchProvider = "firecrawl"
	WebSearchProviderBocha      WebSearchProvider = "bocha"
	WebSearchProviderAliyunIQS  WebSearchProvider = "aliyun_iqs"
	WebSearchProviderBaidu      WebSearchProvider = "baidu"
	WebSearchProviderVolcengine WebSearchProvider = "volcengine"
	WebSearchProviderZhipu      WebSearchProvider = "zhipu"
	WebSearchProviderSearXNG    WebSearchProvider = "searxng"
)

// ValidWebSearchProvider 判断联网搜索服务商是否为已支持取值。
func ValidWebSearchProvider(provider WebSearchProvider) bool {
	switch provider {
	case WebSearchProviderTavily, WebSearchProviderBrave, WebSearchProviderExa, WebSearchProviderPerplexity,
		WebSearchProviderSerper, WebSearchProviderSerpAPI, WebSearchProviderJina, WebSearchProviderFirecrawl,
		WebSearchProviderBocha, WebSearchProviderAliyunIQS, WebSearchProviderBaidu, WebSearchProviderVolcengine,
		WebSearchProviderZhipu, WebSearchProviderSearXNG:
		return true
	default:
		return false
	}
}

// WebSearchProviderSelfHosted 判断服务商是否为企业自行部署、以实例地址接入且不需要 API Key 的服务。
func WebSearchProviderSelfHosted(provider WebSearchProvider) bool {
	return provider == WebSearchProviderSearXNG
}
