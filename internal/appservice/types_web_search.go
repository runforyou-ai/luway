package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// WebSearchProvider 表示联网搜索服务商。
type WebSearchProvider = domain.WebSearchProvider

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
