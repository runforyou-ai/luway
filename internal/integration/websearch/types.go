// Package websearch 调用企业配置的联网搜索服务，把各服务商的结果整理为统一格式。
package websearch

import "github.com/runforyou-ai/cervi/internal/domain"

// Recency 表示搜索结果的发布时间范围。
type Recency string

const (
	RecencyDay   Recency = "day"
	RecencyWeek  Recency = "week"
	RecencyMonth Recency = "month"
	RecencyYear  Recency = "year"
)

const (
	// DefaultCount 是未指定结果条数时返回的条数。
	DefaultCount = 5
	// MaxCount 是单次搜索返回的最大条数。
	MaxCount = 10
)

// Request 定义一次联网搜索。
type Request struct {
	Query   string  `json:"query"`
	Count   int     `json:"count,omitempty"`   // 零值使用 DefaultCount，超过 MaxCount 时按 MaxCount 返回。
	Recency Recency `json:"recency,omitempty"` // 为空表示不限时间。
}

// Item 是一条搜索结果。
type Item struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	SiteName    string `json:"siteName,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"` // 服务商给出的发布时间原文。
}

// Result 是一次联网搜索的结果。
type Result struct {
	Items  []Item `json:"items"`
	Notice string `json:"notice,omitempty"` // 搜索服务未能按请求的时间范围筛选时的说明。
}

// Config 定义调用搜索服务所需的服务商与凭据。
type Config struct {
	Provider domain.WebSearchProvider
	APIKey   string
	BaseURL  string // 自托管服务的实例地址，只对需要实例地址的服务商生效。
}
