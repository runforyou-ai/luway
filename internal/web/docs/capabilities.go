//go:build server

package docs

// Social 是页脚社交入口；空字符串表示不显示对应链接。
type Social struct {
	Github string
	Email  string
}

// Capabilities 控制文档站页头与页脚的商业能力入口，字段与 site.Capabilities 对应。
type Capabilities struct {
	Contact    bool
	Changelog  bool
	Legal      bool
	License    bool
	Newsletter bool
	Social     Social
}
