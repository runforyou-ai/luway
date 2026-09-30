package textsplit

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestSplitCharacterCoverage 验证分段不超过含余量的长度、连续区间、前进不变量和去除重叠后的完整还原。
func TestSplitCharacterCoverage(t *testing.T) {
	sections := make([]string, 0, 10)
	for index := range 10 {
		sections = append(sections, fmt.Sprintf("第 %d 节：唯一标题\n", index)+
			strings.Repeat(fmt.Sprintf("本节序号 %d。正文保留完整。", index), 30))
	}
	// 无标点的连续中文，各位置字符互不相同。
	continuous := make([]rune, 12000)
	for index := range continuous {
		continuous[index] = rune(0x4e00 + index*7919%20000)
	}
	texts := []string{
		strings.Repeat("合同金额 1,234.50 元，不含税。\n    code(x)\n\n🙂备注：不得退款！", 120),
		string(continuous),
		strings.Join(sections, "\n\n"),
		"## 报销标准\n\n| 项目 | 标准 |\n| --- | --- |\n" + strings.Repeat("| 住宿 费用 | 每晚 500 元，含早餐。 |\n", 60) + "\n表格之后的说明。",
		"说明文字。\n\n```go\n" + strings.Repeat("fmt.Println(\"第 1 行 输出, 结果。\")\n# 不是标题\n", 40) + "```\n\n" + strings.Repeat("Follow-up text. ", 80),
		"| " + strings.Repeat("说明 ", 800) + "|\n",
	}
	parameters := []struct{ length, overlap int }{{256, 0}, {256, 30}, {256, 200}, {512, 200}, {2048, 50}}
	for _, parameter := range parameters {
		for textIndex, text := range texts {
			runes := []rune(text)
			segments := Split(text, parameter.length, parameter.overlap)
			if len(segments) == 0 {
				t.Fatalf("Split(text[%d], %d, %d) 未返回分段", textIndex, parameter.length, parameter.overlap)
			}
			reachable := map[int]bool{0: true}
			for position, segment := range segments {
				content := []rune(segment.Content)
				if limit := parameter.length + parameter.length/5; len(content) > limit {
					t.Fatalf("分段 %d 长度 %d 超过含余量的上限 %d", position+1, len(content), limit)
				}
				if segment.CharacterCount != len(content) || segment.Position != position+1 {
					t.Fatalf("分段 %d 的字符数或位置不符：%d, %d", position+1, segment.CharacterCount, segment.Position)
				}
				// 本段起点位于已覆盖位置之前的重叠范围内，且必须推进覆盖位置；重复正文可能存在多个可行起点。
				next := map[int]bool{}
				for covered := range reachable {
					for start := max(0, covered-parameter.overlap); start <= covered; start++ {
						if end := start + len(content); end > covered && end <= len(runes) && slices.Equal(runes[start:end], content) {
							next[end] = true
						}
					}
				}
				if len(next) == 0 {
					t.Fatalf("分段 %d 不是重叠范围内的原文连续区间（length=%d, overlap=%d）", position+1, parameter.length, parameter.overlap)
				}
				reachable = next
			}
			if !reachable[len(runes)] {
				t.Fatalf("text[%d] 分段未覆盖原文 %d 字符（length=%d, overlap=%d）", textIndex, len(runes), parameter.length, parameter.overlap)
			}
		}
	}
}

// TestSplitBoundaryAndHardCut 验证长块按均分长度选择最接近目标的边界，无边界时在目标位置硬切，剩余正文在余量内时整段收入。
func TestSplitBoundaryAndHardCut(t *testing.T) {
	// 602 字均分为三段，目标长度 201，两处句末中第 202 字符处更接近目标。
	multiple := strings.Repeat("甲", 150) + "。" + strings.Repeat("乙", 50) + "。" + strings.Repeat("丙", 400)
	single := strings.Repeat("甲", 200) + "。" + strings.Repeat("乙", 400)
	for _, parameter := range []struct {
		name     string
		text     string
		overlap  int
		expected []int
	}{
		{"最接近均分长度的边界", multiple, 0, []int{202, 200, 200}},
		{"单一边界", single, 0, []int{201, 200, 200}},
		{"带重叠", single, 50, []int{201, 250, 250}},
	} {
		lengths := make([]int, 0, 3)
		for _, segment := range Split(parameter.text, 256, parameter.overlap) {
			lengths = append(lengths, segment.CharacterCount)
		}
		if !slices.Equal(lengths, parameter.expected) {
			t.Fatalf("%s：分段长度 = %v，期望 %v", parameter.name, lengths, parameter.expected)
		}
	}
}

// TestSplitBoundaryPriority 验证收缩范围内按块边界、换行、句末、分句标点和空白的顺序选择边界。
func TestSplitBoundaryPriority(t *testing.T) {
	for _, parameter := range []struct {
		name  string
		text  string
		first int
	}{
		{"换行优先于靠后的空白", strings.Repeat("甲", 149) + "：\n" + strings.Repeat("乙", 50) + " " + strings.Repeat("丙", 400), 151},
		{"空行优先于靠后的换行", strings.Repeat("甲", 129) + "：\n\n" + strings.Repeat("乙", 59) + "：\n" + strings.Repeat("丙", 400), 132},
		{"排版折行让位于句末", strings.Repeat("甲", 140) + "。" + strings.Repeat("乙", 50) + "\n\n" + strings.Repeat("丙", 400), 141},
		{"标点开头的折行让位于句末", strings.Repeat("甲", 140) + "。" + strings.Repeat("乙", 40) + "\n\n，" + strings.Repeat("丙", 400), 141},
		{"英文折行让位于句末", strings.Repeat("a", 150) + ". " + strings.Repeat("b", 60) + "\n" + strings.Repeat("c", 400), 152},
		{"标题不与其后正文分开", strings.Repeat("甲", 139) + "。\n\n## 标题\n\n" + strings.Repeat("乙", 40) + "。" + strings.Repeat("丙", 400), 142},
		{"标题行优先于靠后的换行", strings.Repeat("甲", 130) + "\n## 标题\n" + strings.Repeat("乙", 60) + "\n" + strings.Repeat("丙", 400), 131},
		{"句末优先于靠后的分句标点", strings.Repeat("甲", 140) + "。" + strings.Repeat("乙", 50) + "，" + strings.Repeat("丙", 400), 141},
		{"分句标点优先于靠后的空白", strings.Repeat("甲", 140) + "；" + strings.Repeat("乙", 50) + " " + strings.Repeat("丙", 400), 141},
		{"英文句末在空白之后切分", strings.Repeat("a", 150) + ". " + strings.Repeat("b", 60) + " " + strings.Repeat("c", 400), 152},
		{"小数点不作为句末", strings.Repeat("a", 200) + "3.14" + strings.Repeat("b", 400), 202},
	} {
		if segments := Split(parameter.text, 256, 0); len(segments) == 0 || segments[0].CharacterCount != parameter.first {
			t.Fatalf("%s：首段 = %+v，期望长度 %d", parameter.name, segments[0], parameter.first)
		}
	}
}

// TestSplitToleranceAndBalance 验证余量内的块边界和正文结尾整块收入，超出余量的长段落按均分长度切分。
func TestSplitToleranceAndBalance(t *testing.T) {
	paragraph := strings.Repeat("甲", 279) + "。\n\n"
	if segments := Split(paragraph+strings.Repeat("乙", 400), 256, 0); len(segments) < 2 || segments[0].Content != paragraph {
		t.Fatalf("略超长段落 = %+v", segments)
	}
	if segments := Split(strings.Repeat("甲", 300), 256, 0); len(segments) != 1 {
		t.Fatalf("余量内正文 = %d 段", len(segments))
	}
	lengths := make([]int, 0, 3)
	for _, segment := range Split(strings.Repeat("句子内容。", 80)+"\n\n"+strings.Repeat("乙", 200), 256, 0) {
		lengths = append(lengths, segment.CharacterCount)
	}
	if !slices.Equal(lengths, []int{200, 202, 200}) {
		t.Fatalf("长段落分段长度 = %v", lengths)
	}
}

// TestSplitTableRows 验证表格只在行间切分、从数据行开始的分段附带表头，单行超长时使用行内边界。
func TestSplitTableRows(t *testing.T) {
	header := "| 项目 | 标准 |\n| --- | --- |"
	segments := Split("## 报销标准\n\n"+header+"\n"+strings.Repeat("| 住宿 费用 | 每晚 500 元，含早餐。 |\n", 60), 256, 0)
	if len(segments) < 3 || segments[0].Context != "" {
		t.Fatalf("分段 = %+v", segments)
	}
	for index, segment := range segments {
		if !strings.HasSuffix(segment.Content, "|\n") {
			t.Fatalf("分段 %d 未在行尾结束：%q", index+1, segment.Content)
		}
		if index > 0 && (!strings.HasPrefix(segment.Content, "| 住宿") || segment.Context != "## 报销标准\n"+header) {
			t.Fatalf("分段 %d = %+v", index+1, segment)
		}
	}
	// 带重叠时下一段起点对齐到行首。
	for index, segment := range Split("## 报销标准\n\n"+header+"\n"+strings.Repeat("| 住宿 费用 | 每晚 500 元，含早餐。 |\n", 60), 256, 50)[1:] {
		if !strings.HasPrefix(segment.Content, "| 住宿") {
			t.Fatalf("带重叠分段 %d 未从行首开始：%q", index+2, segment.Content)
		}
	}
	// 表头单元格全部为空时，以首个数据行和分隔行作为表头。
	blank := Split("|  |  |\n| --- | --- |\n| 项目 | 标准 |\n"+strings.Repeat("| 住宿 费用 | 每晚 500 元，含早餐。 |\n", 60), 256, 0)
	if len(blank) < 2 || blank[0].Context != "" || blank[1].Context != "| 项目 | 标准 |\n| --- | --- |" {
		t.Fatalf("空表头分段 = %+v", blank)
	}
	// 行长接近长度上限、行首边界落在最小长度之前时，仍在行间切分。
	wide := "| " + strings.Repeat("甲", 105) + " | " + strings.Repeat("乙", 117) + " |\n"
	for index, segment := range Split("| 项目 | 说明 |\n| --- | --- |\n"+strings.Repeat(wide, 12), 256, 50) {
		if !strings.HasSuffix(segment.Content, "|\n") {
			t.Fatalf("宽表格分段 %d 未在行尾结束：%q", index+1, segment.Content)
		}
	}
	long := Split("| "+strings.Repeat("说明 ", 400)+"|\n", 256, 0)
	if len(long) < 2 || !strings.HasSuffix(long[0].Content, " ") || long[0].Context != "" {
		t.Fatalf("超长单行 = %+v", long)
	}
}

// TestSplitCodeBlock 验证代码块内部只在行间切分，代码块中的井号行不计入标题路径。
func TestSplitCodeBlock(t *testing.T) {
	// 四反引号围栏内的三反引号和井号行都不结束代码块，也不计入标题路径。
	nested := Split("## 示例\n\n````md\n"+strings.Repeat("```\n# 示例标题\n```\n", 30)+"````\n\n"+strings.Repeat("后续正文。", 100), 256, 0)
	for index, segment := range nested {
		if index > 0 && segment.Context != "## 示例" {
			t.Fatalf("嵌套围栏分段 %d 上下文 = %q", index+1, segment.Context)
		}
	}
	text := "## 示例\n\n```go\n" + strings.Repeat("fmt.Println(\"第 1 行 输出, 结果。\")\n# 不是标题\n", 30) + "```\n\n" + strings.Repeat("后续正文。", 100)
	segments := Split(text, 256, 0)
	if len(segments) < 3 {
		t.Fatalf("分段 = %+v", segments)
	}
	for index, segment := range segments {
		if index > 0 && segment.Context != "## 示例" {
			t.Fatalf("分段 %d 上下文 = %q", index+1, segment.Context)
		}
		if index < len(segments)-1 && !strings.HasSuffix(segment.Content, "\n") && !strings.HasSuffix(segment.Content, "后续正文。") {
			t.Fatalf("分段 %d 在行内结束：%q", index+1, segment.Content)
		}
	}
}

// TestSplitHeadingContext 验证上下文取分段起点所属的标题路径，起点为标题行时只保留上级标题。
func TestSplitHeadingContext(t *testing.T) {
	text := "# 员工手册\n" + strings.Repeat("前言内容。", 30) +
		"\n\n## 差旅\n" + strings.Repeat("差旅正文。", 30) +
		"\n\n## 报销\n" + strings.Repeat("报销正文。", 30) +
		"\n\n### 住宿\n" + strings.Repeat("住宿正文。", 100) +
		"\n\n## 其他\n" + strings.Repeat("其他正文。", 100)
	expected := map[string]string{
		"## 差旅":  "# 员工手册",
		"## 报销":  "# 员工手册",
		"### 住宿": "# 员工手册 > ## 报销",
		"住宿正文":   "# 员工手册 > ## 报销 > ### 住宿",
		"## 其他":  "# 员工手册",
		"其他正文":   "# 员工手册 > ## 其他",
	}
	segments := Split(text, 256, 0)
	found := map[string]bool{}
	for _, segment := range segments {
		for prefix, context := range expected {
			if !strings.HasPrefix(strings.TrimLeft(segment.Content, "\n"), prefix) {
				continue
			}
			found[prefix] = true
			if segment.Context != context {
				t.Fatalf("以 %s 开头的分段上下文 = %q，期望 %q", prefix, segment.Context, context)
			}
		}
	}
	if len(segments) == 0 || segments[0].Context != "" || len(found) != len(expected) {
		t.Fatalf("命中 %v，分段 = %+v", found, segments)
	}
}

// TestSplitHeadingBoundary 验证分段不跨越标题、在标题处切开时不带重叠，其余位置的重叠起点对齐到边界。
func TestSplitHeadingBoundary(t *testing.T) {
	text := "## 甲\n" + strings.Repeat("甲甲甲甲。", 70) + "\n\n## 乙\n" + strings.Repeat("乙乙乙乙。", 100)
	segments := Split(text, 256, 50)
	if len(segments) < 3 || !strings.HasPrefix(segments[1].Content, "甲甲甲甲。") || !strings.HasSuffix(segments[1].Content, "。\n\n") ||
		!strings.HasPrefix(segments[2].Content, "## 乙\n") || segments[2].Context != "" {
		t.Fatalf("分段 = %+v", segments)
	}
	for index, segment := range segments {
		if strings.Contains(segment.Content, "甲") && strings.Contains(segment.Content, "乙") {
			t.Fatalf("分段 %d 跨越标题：%q", index+1, segment.Content)
		}
	}
	// 标题前的短行与前一段落合并，不单独成段。
	text = "# 一\n" + strings.Repeat("甲甲甲甲。", 50) + "\n\n" + strings.Repeat("乙乙乙乙。", 50) + "\n\n<!-- 注释 -->\n# 二\n" + strings.Repeat("丙丙丙丙。", 20)
	slides := Split(text, 256, 0)
	if len(slides) != 3 || !strings.HasPrefix(slides[1].Content, "乙") || !strings.HasSuffix(slides[1].Content, "<!-- 注释 -->\n") || !strings.HasPrefix(slides[2].Content, "# 二") {
		t.Fatalf("标题前短行分段 = %+v", slides)
	}
	// 距下一个标题过近的块边界不参与切分，改按行均分。
	lengths := make([]int, 0, 3)
	for _, segment := range Split("# 一\n"+strings.Repeat("甲甲甲甲。\n", 50)+"\n<!-- 注释 -->\n# 二\n"+strings.Repeat("丙", 100), 256, 0) {
		lengths = append(lengths, segment.CharacterCount)
	}
	if !slices.Equal(lengths, []int{160, 157, 104}) {
		t.Fatalf("近标题块边界分段长度 = %v", lengths)
	}
	// 连续的独立短标题各自成段，不并入后续章节。
	faq := Split("## 问题1\n答：可以。\n\n## 问题2\n答：不可以。\n\n## 问题3\n"+strings.Repeat("详细规则。", 100), 256, 0)
	if len(faq) < 3 || !strings.HasPrefix(faq[0].Content, "## 问题1") || strings.Contains(faq[0].Content, "## 问题2") ||
		!strings.HasPrefix(faq[1].Content, "## 问题2") || strings.Contains(faq[1].Content, "## 问题3") {
		t.Fatalf("连续短标题分段 = %+v", faq)
	}
	// 标题前的短前导内容并入该标题所在分段。
	if preamble := Split("<!-- 注释 -->\n# 一\n"+strings.Repeat("甲甲甲甲。", 100), 256, 0); len(preamble) < 2 || !strings.HasPrefix(preamble[0].Content, "<!-- 注释 -->\n# 一\n甲") {
		t.Fatalf("短前导分段 = %+v", preamble)
	}
	// 紧随上级标题的下级标题与上级标题合为一段。
	nested := Split("# 第一章\n\n## 第一节\n"+strings.Repeat("正文内容。", 100), 256, 0)
	if len(nested) < 2 || !strings.HasPrefix(nested[0].Content, "# 第一章\n\n## 第一节\n正文") {
		t.Fatalf("连续标题分段 = %+v", nested)
	}
}

// TestSplitNormalizesNewlines 验证换行规整为 \n 且不折叠代码缩进。
func TestSplitNormalizesNewlines(t *testing.T) {
	segments := Split("第一行\r\n    缩进保留\r第三行", 256, 0)
	if len(segments) != 1 || segments[0].Content != "第一行\n    缩进保留\n第三行" {
		t.Fatalf("Split() = %+v", segments)
	}
}

// TestSplitEmptyResult 验证空白正文和无法前进的长度组合都返回空结果。
func TestSplitEmptyResult(t *testing.T) {
	if segments := Split(" \n\t", 256, 50); segments != nil {
		t.Fatalf("空白正文 = %+v", segments)
	}
	for _, parameter := range []struct{ length, overlap int }{{0, 0}, {50, 50}, {50, 200}} {
		if segments := Split(strings.Repeat("正文", 500), parameter.length, parameter.overlap); segments != nil {
			t.Fatalf("Split(text, %d, %d) = %d 段", parameter.length, parameter.overlap, len(segments))
		}
	}
}
