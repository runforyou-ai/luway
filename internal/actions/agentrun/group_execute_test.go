//go:build server

package agentrun

import (
	"slices"
	"testing"
)

// TestExtractMentionNames 验证从回复正文提取点名成员的边界规则。
func TestExtractMentionNames(t *testing.T) {
	names := []string{"产品经理", "群协作助手 1", "群协作助手 10", "张三", "Dev", "Dev Assistant", "राम"}
	for _, scenario := range []struct {
		name    string
		content string
		want    []string
	}{
		{"开头与空白后的点名按出现顺序去重", "@产品经理 请看。\n@张三。再请 @产品经理 确认", []string{"产品经理", "张三"}},
		{"较长的成员名优先匹配", "@群协作助手 10 和 @群协作助手 1 一起看", []string{"群协作助手 10", "群协作助手 1"}},
		{"成员名后紧跟文字不形成点名", "@产品经理你好", nil},
		{"成员名后紧跟组合标记不形成点名", "@रामू 请看\n@राम 确认", []string{"राम"}},
		{"@ 前紧跟非空白字符不形成点名", "请看，@张三 与 a@产品经理", nil},
		{"名称为另一成员前缀时按完整名称匹配", "@Dev Assistant 请确认", []string{"Dev Assistant"}},
		{"代码中的 @ 不形成点名", "示例 `@张三` 与\n```\n@产品经理\n```\n结束", nil},
		{"波浪线围栏与多反引号行内代码中的 @ 不形成点名", "示例 ``a ` @张三`` 与\n~~~\n@产品经理\n~~~\n结束", nil},
		{"未闭合围栏延续到正文末尾", "开始\n```\n@张三 请看", nil},
		{"缩进代码块中的 @ 不形成点名", "示例：\n\n    @产品经理 请看\n", nil},
		{"代码范围之外的点名照常提取", "`code` @张三 请看", []string{"张三"}},
		{"不在候选中的名称保持普通文字", "@查无此人 请看", nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := extractMentionNames(scenario.content, slices.Clone(names)); !slices.Equal(got, scenario.want) {
				t.Fatalf("点名 = %q，期望 %q", got, scenario.want)
			}
		})
	}
}
