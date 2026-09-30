// Package htmlmarkdown 把 HTML 转换为 CommonMark 与 GFM 表格格式的 Markdown。
package htmlmarkdown

import (
	"context"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
)

// htmlConverter 输出 CommonMark 与 GFM 表格，合并单元格按原值重复填充。
var htmlConverter = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	table.NewTablePlugin(
		table.WithSpanCellBehavior(table.SpanBehaviorMirror),
		table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
		table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
	),
))

// Convert 把 UTF-8 编码的 HTML 转换为 Markdown。
func Convert(ctx context.Context, html string) (string, error) {
	return htmlConverter.ConvertString(html, converter.WithContext(ctx))
}
