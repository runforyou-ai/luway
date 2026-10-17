//go:build server

// Package assets 提供产品站与文档站共用的内联样式与脚本。
package assets

import (
	_ "embed"
	"html/template"
)

//go:embed site.css
var styleSource string

//go:embed site.js
var scriptSource string

// Style 是产品站与文档站内联的样式。
var Style = template.CSS(styleSource)

// Script 是产品站与文档站内联的脚本。
var Script = template.JS(scriptSource)
