package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// WebFetchProvider 表示网页读取服务商。
type WebFetchProvider = domain.WebFetchProvider

// WebFetchSettings 定义企业的网页读取设置；默认使用内置直连抓取。
type WebFetchSettings struct {
	Provider WebFetchProvider `json:"provider"`
	APIKey   string           `json:"apiKey"`
	BaseURL  string           `json:"baseUrl"`
}
