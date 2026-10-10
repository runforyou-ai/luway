// Package docs 内置产品文档的 Markdown 源文件与导航配置。
package docs

import "embed"

// Content 包含中英文文档页面与导航配置。
//
//go:embed nav.yaml zh-cn en
var Content embed.FS
