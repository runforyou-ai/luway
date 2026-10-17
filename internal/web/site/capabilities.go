//go:build server

package site

import "github.com/runforyou-ai/luway/internal/web/docs"

// Social 是页脚社交入口；空字符串表示不显示对应链接。
type Social struct {
	Github string
	Email  string
}

// Capabilities 控制产品站与文档站的商业能力入口；零值对应开源默认（仅首页、下载与文档）。
type Capabilities struct {
	Contact    bool
	Changelog  bool
	Legal      bool
	License    bool
	Newsletter bool
	Social     Social
}

// Pages 返回能力开启时在语言目录下提供的额外页面路径。
func (c Capabilities) Pages() []string {
	var pages []string
	if c.Contact {
		pages = append(pages, "contact")
	}
	if c.Changelog {
		pages = append(pages, "changelog")
	}
	if c.License {
		pages = append(pages, "license-manager")
	}
	if c.Legal {
		pages = append(pages, "privacy", "license-agreement", "terms")
	}
	return pages
}

// DocsCapabilities 返回文档站页头页脚使用的能力开关。
func (c Capabilities) DocsCapabilities() docs.Capabilities {
	return docs.Capabilities{
		Contact: c.Contact, Changelog: c.Changelog, Legal: c.Legal, License: c.License, Newsletter: c.Newsletter,
		Social: docs.Social{Github: c.Social.Github, Email: c.Social.Email},
	}
}
