package appservice

import "github.com/runforyou-ai/cervi/internal/domain"

// WebSearchProvider 表示联网搜索服务商。
type WebSearchProvider string

const (
	WebSearchProviderTavily     WebSearchProvider = WebSearchProvider(domain.WebSearchProviderTavily)
	WebSearchProviderBrave      WebSearchProvider = WebSearchProvider(domain.WebSearchProviderBrave)
	WebSearchProviderExa        WebSearchProvider = WebSearchProvider(domain.WebSearchProviderExa)
	WebSearchProviderPerplexity WebSearchProvider = WebSearchProvider(domain.WebSearchProviderPerplexity)
	WebSearchProviderSerper     WebSearchProvider = WebSearchProvider(domain.WebSearchProviderSerper)
	WebSearchProviderSerpAPI    WebSearchProvider = WebSearchProvider(domain.WebSearchProviderSerpAPI)
	WebSearchProviderJina       WebSearchProvider = WebSearchProvider(domain.WebSearchProviderJina)
	WebSearchProviderFirecrawl  WebSearchProvider = WebSearchProvider(domain.WebSearchProviderFirecrawl)
	WebSearchProviderBocha      WebSearchProvider = WebSearchProvider(domain.WebSearchProviderBocha)
	WebSearchProviderAliyunIQS  WebSearchProvider = WebSearchProvider(domain.WebSearchProviderAliyunIQS)
	WebSearchProviderBaidu      WebSearchProvider = WebSearchProvider(domain.WebSearchProviderBaidu)
	WebSearchProviderVolcengine WebSearchProvider = WebSearchProvider(domain.WebSearchProviderVolcengine)
	WebSearchProviderZhipu      WebSearchProvider = WebSearchProvider(domain.WebSearchProviderZhipu)

	// 以下服务商由企业自行部署，以实例地址接入，不需要 API Key。
	WebSearchProviderSearXNG WebSearchProvider = WebSearchProvider(domain.WebSearchProviderSearXNG)
)

// WebSearchService 定义联网搜索使用的服务商与凭据；自托管服务只填实例地址，其他服务商只填 API Key。
type WebSearchService struct {
	Provider WebSearchProvider `json:"provider"`
	APIKey   string            `json:"apiKey"`
	BaseURL  string            `json:"baseUrl"`
}

// WebSearchSettings 定义企业的联网搜索设置，Service 为空时 AI 不能搜索互联网。
type WebSearchSettings struct {
	Service *WebSearchService `json:"service"`
}
