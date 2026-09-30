package messagepreview

import (
	"strings"
	"testing"
)

// TestText 验证 Markdown 正文按块提取文字、纯文本保持原意，以及空白折叠与长度截取。
func TestText(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		markdown bool
		want     string
	}{
		{name: "纯文本保留标记", body: "**原样**", want: "**原样**"},
		{name: "纯文本保留标题符号", body: "# 访客原文", want: "# 访客原文"},
		{name: "纯文本折叠换行", body: "第一行\n\n第二行", want: "第一行 第二行"},
		{name: "标题链接与行内代码", body: "# 标题\n\n[链接](https://example.com) 和 `代码`", markdown: true, want: "标题 链接 和 代码"},
		{name: "列表条目", body: "- 第一项\n- 第二项", markdown: true, want: "第一项 第二项"},
		{name: "引用与软换行", body: "> 引用第一行\n> 引用第二行", markdown: true, want: "引用第一行 引用第二行"},
		{name: "表格单元格", body: "| 名称 | 数量 |\n| --- | --- |\n| 苹果 | 3 |", markdown: true, want: "名称 数量 苹果 3"},
		{name: "代码块与原始 HTML", body: "```go\nfmt.Println(1)\n```\n\n<div>忽略</div>\n\n结尾 <b>加粗</b>", markdown: true, want: "fmt.Println(1) 结尾 加粗"},
		{name: "图片替代文本与链接定义", body: "![示意图](a.png) [参考][1]\n\n[1]: https://example.com", markdown: true, want: "示意图 参考"},
		{name: "强调与删除线", body: "**重点** 和 ~~删除~~", markdown: true, want: "重点 和 删除"},
		{name: "转义字符与字符引用", body: `Tom &amp; Jerry \*字面星号\* &#169;`, markdown: true, want: "Tom & Jerry *字面星号* ©"},
		{name: "转义后的字符引用保持原文", body: `\&amp; \&#169;`, markdown: true, want: "&amp; &#169;"},
		{name: "字符引用只解码一次", body: "&#38;amp; &amp;#169;", markdown: true, want: "&amp; &#169;"},
		{name: "行内代码保留原文", body: "`a &amp; \\*`", markdown: true, want: `a &amp; \*`},
	}
	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			if got := Text(current.body, current.markdown); got != current.want {
				t.Fatalf("Text(%q) = %q, want %q", current.body, got, current.want)
			}
		})
	}
}

// TestTextTruncatesByRune 验证摘要按字符截取，不截断多字节字符。
func TestTextTruncatesByRune(t *testing.T) {
	got := Text(strings.Repeat("中", MaxRunes+10), false)
	if got != strings.Repeat("中", MaxRunes) {
		t.Fatalf("摘要长度 = %d 个字符", len([]rune(got)))
	}
}
