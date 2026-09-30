package searchtext

import (
	"slices"
	"strings"
	"testing"
)

// TestVectorLexemes 验证单字、字母数字片段和多音字读音共用字符位置。
func TestVectorLexemes(t *testing.T) {
	vector := Vector("型号E-731，长春")
	for _, lexeme := range []string{"'型':1", "'号':2", "'e':3", "'731':4", "'长':5", "'~chang':5", "'~zhang':5", "'春':6", "'~chun':6"} {
		if !strings.Contains(vector, lexeme) {
			t.Fatalf("Vector 缺少 %s：%s", lexeme, vector)
		}
	}
	if Vector("", "，。") != "" {
		t.Fatalf("无可检索内容时应返回空 tsvector")
	}
}

// TestVectorSeparatesTexts 验证多段文本之间空出位置，全角字符规范化为半角小写。
func TestVectorSeparatesTexts(t *testing.T) {
	vector := Vector("报告", "Ｑ3.PDF")
	for _, lexeme := range []string{"'报':1", "'告':2", "'q':4", "'3':5", "'pdf':6"} {
		if !strings.Contains(vector, lexeme) {
			t.Fatalf("Vector 缺少 %s：%s", lexeme, vector)
		}
	}
}

// TestParseQuery 验证检索词拆分、短语、字母前缀、拼音切分和末个音节的完整音节展开。
func TestParseQuery(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "长春", expected: "(('长' <-> '春'))"},
		{input: "E-731", expected: "(('e' <-> '731':*))"},
		{input: "报销 3月", expected: "(('报' <-> '销')) & (('3' <-> '月'))"},
		{input: "changchun", expected: "(('changchun':*) | ('~chang' <-> '~chun'))"},
		{input: "huan", expected: "(('huan':*) | (('~huan' | '~huang')))"},
	}
	for _, item := range cases {
		query, ok := ParseQuery(item.input)
		if !ok {
			t.Fatalf("ParseQuery(%q) 未返回检索词", item.input)
		}
		if actual := query.TSQuery(); actual != item.expected {
			t.Fatalf("ParseQuery(%q).TSQuery() = %s，期望 %s", item.input, actual, item.expected)
		}
	}
	if _, ok := ParseQuery(" ，。 "); ok {
		t.Fatalf("只有标点和空白时不应返回检索词")
	}
}

// TestSplitPinyin 验证多种切分、元音开头音节只作首音节和正在输入的末个音节。
func TestSplitPinyin(t *testing.T) {
	cases := map[string][]string{
		"xian":    {"xian"},
		"fapiao":  {"fa piao"},
		"anquan":  {"an quan"},
		"shangan": {"shan gan"},
	}
	for word, expected := range cases {
		joined := []string{}
		for _, split := range splitPinyin(word) {
			joined = append(joined, strings.Join(split, " "))
		}
		if !slices.Equal(joined, expected) {
			t.Fatalf("splitPinyin(%s) = %v，期望 %v", word, joined, expected)
		}
	}
	partial := splitPinyin("changch")
	if len(partial) == 0 || strings.Join(partial[0], " ") != "chang ch" {
		t.Fatalf("splitPinyin(changch) = %v，期望末段取音节开头", partial)
	}
	if splitPinyin("qwv") != nil {
		t.Fatalf("无法切分为拼音的字母不应返回切分方案")
	}
}

// TestExcerpt 验证拼音、编号、多检索词命中位置和长文本省略。
func TestExcerpt(t *testing.T) {
	cases := []struct {
		query, text string
		expected    []Segment
	}{
		{query: "changchun", text: "明天去长春出差", expected: []Segment{{Text: "明天去"}, {Text: "长春", Match: true}, {Text: "出差"}}},
		{query: "changchu", text: "明天去长春出差", expected: []Segment{{Text: "明天去"}, {Text: "长春", Match: true}, {Text: "出差"}}},
		{query: "e731", text: "型号E-731故障", expected: []Segment{{Text: "型号"}, {Text: "E-731", Match: true}, {Text: "故障"}}},
		{query: "报销 发票", text: "发票已提交\n报销中", expected: []Segment{{Text: "发票", Match: true}, {Text: "已提交 "}, {Text: "报销", Match: true}, {Text: "中"}}},
		{query: "合同", text: strings.Repeat("前", 20) + "合同", expected: []Segment{{Text: "…" + strings.Repeat("前", 12)}, {Text: "合同", Match: true}}},
	}
	for _, item := range cases {
		query, _ := ParseQuery(item.query)
		segments, ok := query.Excerpt(item.text)
		if !ok || !slices.Equal(segments, item.expected) {
			t.Fatalf("Excerpt(%q, %q) = %+v，期望 %+v", item.query, item.text, segments, item.expected)
		}
	}
	query, _ := ParseQuery("退款")
	if _, ok := query.Excerpt("发票已提交"); ok {
		t.Fatalf("未命中的文本不应返回摘要")
	}
}

// TestParseKeywords 验证每个检索词整段按相邻位置匹配，检索词之间满足其一，不丢弃常用词也不拆成单字。
func TestParseKeywords(t *testing.T) {
	for input, want := range map[string]string{
		"处理结果":       "(('处' <-> '理' <-> '结' <-> '果'))",
		"已退款":        "(('已' <-> '退' <-> '款'))",
		"退款 结果":      "(('退' <-> '款')) | (('结' <-> '果'))",
		"E-731 什么时候": "(('e' <-> '731')) | (('什' <-> '么' <-> '时' <-> '候'))",
	} {
		query, ok := ParseKeywords(input)
		if !ok || query.TSQuery() != want {
			t.Fatalf("%q => %q, want %q", input, query.TSQuery(), want)
		}
	}
	if _, ok := ParseKeywords(" ，。 "); ok {
		t.Fatal("punctuation should not be searchable")
	}
}

// TestWindow 验证超长原文围绕首个命中截取，未命中时从开头截取，短文本原样返回。
func TestWindow(t *testing.T) {
	query, _ := ParseKeywords("REVIEW731")
	text := strings.Repeat("前", 550) + "订单 REVIEW731 已到账" + strings.Repeat("后", 550)
	window := query.Window(text, 100)
	if !strings.HasPrefix(window, "…") || !strings.HasSuffix(window, "…") || !strings.Contains(window, "REVIEW731 已到账") || len([]rune(window)) != 102 {
		t.Fatalf("window = %q", window)
	}
	if head := query.Window(strings.Repeat("无", 200), 100); head != strings.Repeat("无", 100)+"…" {
		t.Fatalf("head = %q", head)
	}
	if short := query.Window("REVIEW731", 100); short != "REVIEW731" {
		t.Fatalf("short = %q", short)
	}
}
