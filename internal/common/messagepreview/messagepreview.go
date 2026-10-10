// Package messagepreview 生成会话列表和消息提醒使用的单行纯文本摘要。
package messagepreview

import (
	"bytes"
	"strings"

	"github.com/runforyou-ai/support/str"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MaxRunes 是摘要保留的最大字符数。
const MaxRunes = 200

// markdownParser 按 GFM 语法解析 AI 回复正文。
var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

// Text 返回单行摘要：Markdown 正文提取块级文字并以空格分隔，其余正文保留原文；空白折叠后截取前 MaxRunes 个字符。
func Text(body string, markdown bool) string {
	if markdown {
		body = markdownText([]byte(body))
	}
	return str.Substr(str.Squish(body), 0, MaxRunes)
}

// markdownText 展开列表、引用和表格等容器块，逐块提取文字，原始 HTML 不计入。
func markdownText(source []byte) string {
	var parts []string
	pending := children(markdownParser.Parse(text.NewReader(source)))
	for len(pending) > 0 {
		node := pending[0]
		pending = pending[1:]
		switch node.Kind() {
		case ast.KindList, ast.KindListItem, ast.KindBlockquote, extast.KindTable, extast.KindTableHeader, extast.KindTableRow:
			pending = append(children(node), pending...)
		case ast.KindHTMLBlock, ast.KindThematicBreak:
		case ast.KindCodeBlock, ast.KindFencedCodeBlock:
			var code strings.Builder
			lines := node.Lines()
			for index := range lines.Len() {
				segment := lines.At(index)
				code.Write(segment.Value(source))
			}
			parts = append(parts, code.String())
		default:
			var block strings.Builder
			inlineText(&block, node, source)
			parts = append(parts, block.String())
		}
	}
	return strings.Join(parts, " ")
}

// children 返回节点的直接子节点。
func children(node ast.Node) []ast.Node {
	var nodes []ast.Node
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		nodes = append(nodes, child)
	}
	return nodes
}

// inlineText 按文档顺序写入行内文字，转义字符和字符引用按显示文字解码，行内代码保留原文，链接保留文字、图片保留替代文本、原始 HTML 不计入。
func inlineText(out *strings.Builder, node ast.Node, source []byte) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch current := child.(type) {
		case *ast.CodeSpan:
			for code := current.FirstChild(); code != nil; code = code.NextSibling() {
				if text, ok := code.(*ast.Text); ok {
					out.Write(text.Segment.Value(source))
				}
			}
		case *ast.Text:
			writeText(out, current.Segment.Value(source))
			if current.SoftLineBreak() || current.HardLineBreak() {
				out.WriteByte(' ')
			}
		case *ast.String:
			out.Write(current.Value)
		case *ast.AutoLink:
			out.Write(current.Label(source))
		case *ast.RawHTML:
		default:
			inlineText(out, child, source)
		}
	}
}

// writeText 单遍解码反斜杠转义和字符引用，解码产生的字符原样输出。
func writeText(out *strings.Builder, value []byte) {
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\\':
			if index+1 < len(value) && util.IsPunct(value[index+1]) {
				index++
			}
		case '&':
			// 字符引用由 & 开始，经字母、数字或 # 到分号结束，每次只解码这一个引用。
			end := index + 1
			for end < len(value) && (util.IsAlphaNumeric(value[end]) || value[end] == '#') {
				end++
			}
			if end < len(value) && value[end] == ';' {
				reference := value[index : end+1]
				if decoded := util.ResolveEntityNames(util.ResolveNumericReferences(reference)); !bytes.Equal(decoded, reference) {
					out.Write(decoded)
					index = end
					continue
				}
			}
		}
		out.WriteByte(value[index])
	}
}
