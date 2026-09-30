// Package textsplit 按 Markdown 结构、字符长度和重叠切分正文。
package textsplit

import (
	"regexp"
	"strings"
	"unicode"
)

// Segment 表示一段按文档顺序编号的正文，上下文为分段起点所属的标题路径和表头。
type Segment struct {
	Position       int
	Context        string
	Content        string
	CharacterCount int
}

// 切分边界等级，数值越大越优先。
const (
	boundaryNone int8 = iota
	boundaryEnclosed
	boundarySpace
	boundaryClause
	boundarySentence
	boundaryLine
	boundaryBlock
)

type lineKind int8

const (
	lineText lineKind = iota
	lineBlank
	lineHeading
	lineTable
	lineFence
	lineCode
)

var (
	headingPattern   = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]|$)`)
	delimiterPattern = regexp.MustCompile(`^ {0,3}\|[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
)

type heading struct {
	start, level int
	text         string
	attached     bool
}

type table struct {
	start, rows, dataStart, end int
	header, delimiter           string
}

// Split 按目标长度切分正文，长度和重叠均按 Unicode 字符计数，上下文不计入长度。
// 分段正文是规整换行后原文的连续区间，相邻分段重叠不超过重叠长度，去除重叠后可逐字符还原。
// 边界依次优先块边界、换行、句末、分句标点和空白；表格与代码块内部只在行间切分，单行超长时才使用行内边界。
// 长度上限外留 20% 余量：到下一个标题的剩余正文在余量内时整体收入；余量内有块边界时选择最接近均分目标的块边界，否则按当前块剩余正文均分切分。
// 独立标题是硬边界，分段不跨越标题，在标题处切开时不带重叠。
// 正文为空白，或长度不大于重叠导致分段无法前进时，返回空结果。
func Split(text string, length, overlap int) []Segment {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if length <= overlap || strings.TrimSpace(normalized) == "" {
		return nil
	}
	runes := []rune(normalized)
	levels, headings, tables := markBoundaries(runes)
	// 记录每个位置之后最近的块边界，正文末尾视为块边界。
	nextBlock := make([]int, len(runes)+1)
	nextBlock[len(runes)] = len(runes)
	for index := len(runes) - 1; index >= 0; index-- {
		nextBlock[index] = nextBlock[index+1]
		if levels[index] == boundaryBlock {
			nextBlock[index] = index
		}
	}

	// 记录每个位置及之后最近的独立标题起点，紧随标题的标题不作为边界，正文末尾视为标题起点。
	hardStarts := make([]int, len(runes)+1)
	nearest, position := len(runes), len(headings)-1
	for index := len(runes); index >= 0; index-- {
		for ; position >= 0 && headings[position].start >= index; position-- {
			if !headings[position].attached {
				nearest = headings[position].start
			}
		}
		hardStarts[index] = nearest
	}

	minimum := max(overlap+1, length/2)
	limit := length + length/5
	var segments []Segment
	var stack []heading
	nextHeading, currentTable := 0, 0
	for start := 0; start < len(runes); {
		// 分段不跨越独立标题，下一个标题或正文末尾在余量内时直接切到该处；标题前不超过余量长度的前导内容并入该标题所在分段。
		end := hardStarts[start+1]
		if hardStarts[start] != start && end < len(runes) && end-start <= length/5 {
			end = hardStarts[end+1]
		}
		if span := end - start; span > limit {
			// 余量内有块边界时均分到下一个标题的剩余正文，否则均分当前块的剩余正文，按段数估算目标位置。
			remaining, upper := nextBlock[start+minimum]-start, start+length
			if remaining <= limit {
				remaining, upper = span, start+limit
			}
			pieces := (remaining - overlap + length - overlap - 1) / (length - overlap)
			target := start + (remaining+(pieces-1)*overlap+pieces-1)/pieces
			// 选择等级最高且最接近目标的边界，长度上限外只接受块边界，距下一个标题不超过余量长度的边界不参与，无边界时在目标位置硬切。
			best, bestLevel := target, boundaryNone
			for index := start + minimum; index <= upper; index++ {
				if level := levels[index]; (index <= start+length || level == boundaryBlock) && end-index > length/5 &&
					(level > bestLevel || level != boundaryNone && level == bestLevel && max(index-target, target-index) < max(best-target, target-best)) {
					best, bestLevel = index, level
				}
			}
			// 只找到表格或代码块的行内边界时，退到最小长度之前最靠后的行级边界，只有整行放不下才在行内切分。
			if bestLevel <= boundaryEnclosed {
				for index := start + minimum - 1; index > start; index-- {
					if levels[index] >= boundaryLine {
						best, bestLevel = index, levels[index]
						break
					}
				}
			}
			end = best
		}
		// 标题路径取正文首个非换行字符之前的标题层级，该位置恰为标题行时只保留其上级标题。
		anchor := start
		for anchor < end && runes[anchor] == '\n' {
			anchor++
		}
		for nextHeading < len(headings) && headings[nextHeading].start < anchor {
			item := headings[nextHeading]
			for len(stack) > 0 && stack[len(stack)-1].level >= item.level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, item)
			nextHeading++
		}
		path := stack
		if nextHeading < len(headings) && headings[nextHeading].start == anchor {
			for len(path) > 0 && path[len(path)-1].level >= headings[nextHeading].level {
				path = path[:len(path)-1]
			}
		}
		parts := make([]string, 0, 2)
		if len(path) > 0 {
			names := make([]string, 0, len(path))
			for _, item := range path {
				names = append(names, item.text)
			}
			parts = append(parts, strings.Join(names, " > "))
		}
		// 起点位于表格数据行时附带表头和分隔行。
		for currentTable < len(tables) && tables[currentTable].end <= anchor {
			currentTable++
		}
		if currentTable < len(tables) && tables[currentTable].header != "" && tables[currentTable].dataStart <= anchor {
			parts = append(parts, tables[currentTable].header)
		}
		segments = append(segments, Segment{
			Position:       len(segments) + 1,
			Context:        strings.Join(parts, "\n"),
			Content:        string(runes[start:end]),
			CharacterCount: end - start,
		})
		if end == len(runes) {
			break
		}
		// 在标题处切开时不带重叠；否则下一段起点在重叠范围内选择等级最高且最靠前的边界，无边界时取重叠长度处。
		next, nextLevel := end-overlap, boundaryNone
		if next <= start || hardStarts[end] == end {
			next = end
		}
		// 重叠范围末尾的空白不作为下一段起点。
		tail := end
		for tail > next && unicode.IsSpace(runes[tail-1]) {
			tail--
		}
		for index := next; index < tail; index++ {
			if levels[index] > nextLevel {
				next, nextLevel = index, levels[index]
			}
		}
		// 重叠范围内只有表格或代码块的行内边界时不带重叠。
		if nextLevel == boundaryEnclosed {
			next = end
		}
		start = next
	}
	return segments
}

// markBoundaries 逐行识别 Markdown 结构，返回每个切分位置的边界等级、标题和表格。
func markBoundaries(runes []rune) ([]int8, []heading, []table) {
	levels := make([]int8, len(runes)+1)
	var headings []heading
	var tables []table
	var previousKind lineKind
	var fence string
	var wrapRune rune
	var blankStarts []int
	var previousHeading bool
	for lineStart, lineIndex := 0, 0; lineStart <= len(runes); lineIndex++ {
		lineEnd := lineStart
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}
		line := string(runes[lineStart:lineEnd])
		trimmed := strings.TrimLeft(line, " ")
		// 识别代码围栏、代码、空行、标题和表格行。
		kind := lineText
		run := fenceRun(trimmed)
		switch {
		case fence != "":
			kind = lineCode
			// 闭合围栏与开启围栏同种且不短于开启围栏，其后只允许空白。
			if len(run) >= len(fence) && run[0] == fence[0] && strings.TrimRight(trimmed[len(run):], " \t") == "" {
				kind, fence = lineFence, ""
			}
		case len(run) >= 3:
			kind, fence = lineFence, run
		case strings.TrimSpace(line) == "":
			kind = lineBlank
		case headingPattern.MatchString(line):
			kind = lineHeading
			headings = append(headings, heading{start: lineStart, level: len(headingPattern.FindStringSubmatch(line)[1]), text: strings.TrimRight(line, " \t"), attached: previousHeading})
		case strings.HasPrefix(trimmed, "|"):
			kind = lineTable
		}
		if lineIndex > 0 {
			levels[lineStart] = boundaryLine
			if kind != lineCode && (kind == lineHeading || previousKind == lineBlank && kind != lineBlank ||
				kind == lineFence && fence != "" || previousKind == lineFence && kind != lineCode ||
				(kind == lineTable) != (previousKind == lineTable)) {
				levels[lineStart] = boundaryBlock
			}
			// 前一正文行以文字结尾且本行以文字或句中、句末标点开头时，换行及其间空行视为排版折行，按空白处理。
			if first := []rune(strings.TrimLeft(line, " \t")); kind == lineText && wrapRune != 0 && len(first) > 0 &&
				(unicode.IsLetter(wrapRune) || unicode.IsNumber(wrapRune)) &&
				(unicode.IsLetter(first[0]) || unicode.IsNumber(first[0]) || strings.ContainsRune("，。、；：！？）」』》,.;:!?)", first[0])) {
				for _, index := range append(blankStarts, lineStart) {
					levels[index] = boundarySpace
				}
			}
		}
		// 标题行与其后首个非空行之间不作为切分位置。
		if kind != lineBlank && previousHeading {
			for _, index := range append(blankStarts, lineStart) {
				levels[index] = boundaryNone
			}
		}
		if kind != lineBlank {
			previousHeading = kind == lineHeading
		}
		switch kind {
		case lineText:
			trailing := []rune(strings.TrimRight(line, " \t"))
			wrapRune, blankStarts = trailing[len(trailing)-1], blankStarts[:0]
		case lineBlank:
			blankStarts = append(blankStarts, lineStart)
		default:
			wrapRune, blankStarts = 0, blankStarts[:0]
		}
		// 表格第二行为分隔行时，前两行作为表头；表头单元格全部为空时改用首个数据行与分隔行作为表头。
		if kind == lineTable {
			if previousKind != lineTable {
				tables = append(tables, table{start: lineStart, dataStart: len(runes) + 1})
			}
			current := &tables[len(tables)-1]
			current.end = lineEnd
			switch {
			case current.rows == 1 && delimiterPattern.MatchString(line):
				current.header, current.dataStart = string(runes[current.start:lineEnd]), lineEnd+1
				if strings.Trim(string(runes[current.start:lineStart]), "| \t\n") == "" {
					current.header, current.delimiter = "", line
				}
			case current.rows == 2 && current.delimiter != "":
				current.header, current.dataStart = line+"\n"+current.delimiter, lineEnd+1
			}
			current.rows++
		}
		// 标注行内句末、分句标点和空白边界，表格、代码与围栏行内降为最低等级。
		for index := lineStart + 1; index <= lineEnd; index++ {
			level := boundaryNone
			switch runes[index-1] {
			case '。', '！', '？':
				level = boundarySentence
			case '；', '，':
				level = boundaryClause
			case ' ', '\t':
				level = boundarySpace
				if index-2 >= lineStart {
					switch runes[index-2] {
					case '.', '!', '?':
						level = boundarySentence
					case ';', ',':
						level = boundaryClause
					}
				}
			}
			if level != boundaryNone && (kind == lineTable || kind == lineCode || kind == lineFence) {
				level = boundaryEnclosed
			}
			levels[index] = level
		}
		previousKind = kind
		lineStart = lineEnd + 1
	}
	return levels, headings, tables
}

// fenceRun 返回行首连续的反引号或波浪号围栏标记。
func fenceRun(line string) string {
	if line == "" || line[0] != '`' && line[0] != '~' {
		return ""
	}
	index := 0
	for index < len(line) && line[index] == line[0] {
		index++
	}
	return line[:index]
}

// IndexText 返回上下文与正文拼接后的文本，上下文非空时置于正文之前。
func IndexText(context, content string) string {
	if context == "" {
		return content
	}
	return context + "\n\n" + content
}
