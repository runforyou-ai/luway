//go:build server

package productdocs

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/runforyou-ai/support/str"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"go.abhg.dev/goldmark/anchor"
	"go.abhg.dev/goldmark/frontmatter"
	"go.abhg.dev/goldmark/toc"
)

// frontMatter 是文档页首声明的标题与排序。
type frontMatter struct {
	Title string `yaml:"title"`
	Order int    `yaml:"order"`
}

// Heading 是页面中的一个标题，Level 为 1 至 6。
type Heading struct {
	Level int
	ID    string
	Text  string
}

// rendered 是单个 Markdown 文件的渲染结果。
type rendered struct {
	meta     frontMatter
	html     string
	text     string
	headings []Heading
	links    []string
}

// markdown 是文档统一使用的 goldmark 实例：GFM、frontmatter、标题锚点、代码高亮与提示块。
var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		&frontmatter.Extender{},
		&anchor.Extender{Texter: anchor.Text("#"), Position: anchor.After},
		highlighting.NewHighlighting(highlighting.WithFormatOptions(chromahtml.WithClasses(true))),
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(calloutTransformer{}, 100)),
	),
)

// localeContextKey 在解析上下文中保存当前页面的语言目录。
var localeContextKey = parser.NewContextKey()

// calloutTitles 是各语言的提示块标题，键为 GitHub 提示块类型的小写形式。
var calloutTitles = map[string]map[string]string{
	"zh-cn": {"note": "注意", "tip": "提示", "important": "重要", "warning": "警告", "caution": "当心"},
	"en":    {"note": "Note", "tip": "Tip", "important": "Important", "warning": "Warning", "caution": "Caution"},
}

// calloutTransformer 把以 [!NOTE] 等标记开头的引用块转为带本地化标题的提示块。
type calloutTransformer struct{}

// Transform 去掉引用块首行的类型标记，为引用块加上提示块类名并在开头插入标题段落。
func (calloutTransformer) Transform(document *ast.Document, reader text.Reader, context parser.Context) {
	source := reader.Source()
	locale, _ := context.Get(localeContextKey).(string)
	var quotes []*ast.Blockquote
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if quote, ok := node.(*ast.Blockquote); ok && entering {
			quotes = append(quotes, quote)
		}
		return ast.WalkContinue, nil
	})
	for _, quote := range quotes {
		paragraph, ok := quote.FirstChild().(*ast.Paragraph)
		if !ok {
			continue
		}
		// GFM 把 [!NOTE] 解析为三个相邻文本节点：「[」「!NOTE」「]」。
		var marker strings.Builder
		var markerNodes []ast.Node
		for node := paragraph.FirstChild(); node != nil && len(markerNodes) < 3; node = node.NextSibling() {
			textNode, ok := node.(*ast.Text)
			if !ok {
				break
			}
			marker.Write(textNode.Segment.Value(source))
			markerNodes = append(markerNodes, node)
			if strings.HasSuffix(marker.String(), "]") {
				break
			}
		}
		kind, ok := strings.CutPrefix(marker.String(), "[!")
		kind = strings.ToLower(strings.TrimSuffix(kind, "]"))
		title := calloutTitles[locale][kind]
		if !ok || title == "" || !strings.HasSuffix(marker.String(), "]") {
			continue
		}
		for _, node := range markerNodes {
			paragraph.RemoveChild(paragraph, node)
		}
		// 标记独占首行时去掉随之留下的空段落。
		if paragraph.ChildCount() == 0 {
			quote.RemoveChild(quote, paragraph)
		}
		quote.SetAttributeString("class", "docs-callout docs-callout-"+kind)
		heading := ast.NewParagraph()
		heading.SetAttributeString("class", "docs-callout-title")
		heading.AppendChild(heading, ast.NewString([]byte(title)))
		quote.InsertBefore(quote, quote.FirstChild(), heading)
	}
}

// render 解析并渲染指定语言目录下的一页 Markdown，收集标题、纯文本与站内链接。
func render(locale string, source []byte) (rendered, error) {
	context := parser.NewContext(parser.WithIDs(&headingIDs{used: map[string]int{}}))
	context.Set(localeContextKey, locale)
	document := markdown.Parser().Parse(text.NewReader(source), parser.WithContext(context))
	var result rendered
	if data := frontmatter.Get(context); data != nil {
		if err := data.Decode(&result.meta); err != nil {
			return rendered{}, fmt.Errorf("decode front matter: %w", err)
		}
	}
	var html bytes.Buffer
	if err := markdown.Renderer().Render(&html, source, document); err != nil {
		return rendered{}, fmt.Errorf("render markdown: %w", err)
	}
	result.html = html.String()
	tree, err := toc.Inspect(document, source)
	if err != nil {
		return rendered{}, fmt.Errorf("inspect headings: %w", err)
	}
	result.headings = flattenHeadings(tree.Items, 1, nil)
	var plain strings.Builder
	// 收集正文纯文本（行内代码的文本是其 Text 子节点）与链接地址，供搜索与链接校验使用。
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.Text:
			plain.Write(node.Segment.Value(source))
			if node.SoftLineBreak() || node.HardLineBreak() {
				plain.WriteByte(' ')
			}
		case *ast.String:
			plain.Write(node.Value)
		case *ast.Link:
			result.links = append(result.links, string(node.Destination))
		case *ast.Paragraph, *ast.Heading, *ast.ListItem:
			plain.WriteByte(' ')
		}
		return ast.WalkContinue, nil
	})
	result.text = str.Squish(plain.String())
	return result, nil
}

// flattenHeadings 按文档顺序展开目录树，跳过 toc 为补齐层级插入的空标题。
func flattenHeadings(items toc.Items, level int, headings []Heading) []Heading {
	for _, item := range items {
		if len(item.ID) > 0 {
			headings = append(headings, Heading{Level: level, ID: string(item.ID), Text: string(item.Title)})
		}
		headings = flattenHeadings(item.Items, level+1, headings)
	}
	return headings
}

// headingIDs 生成保留中文等 Unicode 字母的标题编号，同一页内重复时追加序号。
type headingIDs struct {
	used map[string]int
}

// Generate 把标题文本转为小写、空白换成连字符并去掉标点后的编号。
func (ids *headingIDs) Generate(value []byte, _ ast.NodeKind) []byte {
	var slug strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(string(value))) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			slug.WriteRune(r)
		case unicode.IsSpace(r):
			slug.WriteByte('-')
		}
	}
	id := slug.String()
	if id == "" {
		id = "section"
	}
	count := ids.used[id]
	ids.used[id] = count + 1
	if count > 0 {
		id += "-" + strconv.Itoa(count)
	}
	return []byte(id)
}

// Put 记录文档中显式指定的标题编号。
func (ids *headingIDs) Put(value []byte) {
	ids.used[string(value)]++
}
