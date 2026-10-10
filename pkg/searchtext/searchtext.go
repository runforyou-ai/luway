// Package searchtext 为 PostgreSQL 全文检索生成中文 tsvector 词元、tsquery 条件和命中摘要。
package searchtext

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// maxWordRunes 是字母或数字连续片段写入词元的最大字符数。
	maxWordRunes = 64
	// maxLexemePositions 是单个词元记录的位置数量上限。
	maxLexemePositions = 255
	// maxPosition 是 tsvector 位置值上限。
	maxPosition = 16383
	// pinyinPrefix 标记汉字读音词元，与字母片段词元区分。
	pinyinPrefix = "~"
)

// slot 表示占用一个词元位置的检索单元及其在原文中的字符区间。
type slot struct {
	lexemes    []string
	word       bool
	start, end int
}

// tokenize 按字规范化原文，切分为单字和字母、数字连续片段，标点与空白不占位置；pinyin 为真时汉字附带读音词元。
func tokenize(text string, pinyin bool) []slot {
	var slots []slot
	var word []rune
	wordStart, wordEnd := 0, 0
	flush := func() {
		if len(word) > 0 && len(word) <= maxWordRunes {
			slots = append(slots, slot{lexemes: []string{string(word)}, word: true, start: wordStart, end: wordEnd})
		}
		word = word[:0]
	}
	for index, original := range []rune(text) {
		for _, r := range norm.NFKC.String(string(original)) {
			r = unicode.ToLower(r)
			switch {
			case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
				flush()
				lexemes := []string{string(r)}
				if pinyin {
					lexemes = append(lexemes, pinyinLexemes(r)...)
				}
				slots = append(slots, slot{lexemes: lexemes, start: index, end: index + 1})
			case unicode.IsLetter(r) || unicode.IsNumber(r):
				// 字母与数字交界处拆成两个片段，使 E731 与 E-731 得到相同词元。
				if len(word) > 0 && unicode.IsNumber(word[len(word)-1]) != unicode.IsNumber(r) {
					flush()
				}
				if len(word) == 0 {
					wordStart = index
				}
				word = append(word, r)
				wordEnd = index + 1
			default:
				flush()
			}
		}
	}
	flush()
	return slots
}

// Vector 返回多段文本的 tsvector 字面量，相邻文本之间空出一个位置，短语不跨段匹配。
func Vector(texts ...string) string {
	positions := map[string][]int{}
	position := 0
	for _, text := range texts {
		for _, item := range tokenize(text, true) {
			position++
			for _, lexeme := range item.lexemes {
				addPosition(positions, lexeme, position)
			}
		}
		position++
	}
	return vectorLiteral(positions)
}

// addPosition 记录词元出现位置，超出单个词元的位置数量或位置值上限时截断。
func addPosition(positions map[string][]int, lexeme string, position int) {
	if len(positions[lexeme]) < maxLexemePositions {
		positions[lexeme] = append(positions[lexeme], min(position, maxPosition))
	}
}

// vectorLiteral 把词元位置表转成 tsvector 字面量。
func vectorLiteral(positions map[string][]int) string {
	var builder strings.Builder
	for _, lexeme := range slices.Sorted(maps.Keys(positions)) {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		// 词元只由字母、数字、汉字和读音前缀组成，不含需要转义的引号或反斜杠。
		builder.WriteString("'" + lexeme + "':")
		for index, value := range positions[lexeme] {
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(strconv.Itoa(value))
		}
	}
	return builder.String()
}
