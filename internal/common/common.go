// Package common 提供无数据库、无传输层和无平台依赖的通用能力。
package common

import "github.com/runforyou-ai/cervi/internal/common/brand"

// WebFetchUserAgent 返回抓取公开网页时以品牌标识表明身份的 User-Agent。
func WebFetchUserAgent() string {
	return "Mozilla/5.0 (compatible; " + brand.Build().Slug + "/1.0)"
}
