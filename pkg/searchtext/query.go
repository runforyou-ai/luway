package searchtext

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/runforyou-ai/support/arr"
)

const (
	// excerptLeading 是摘要中首个命中之前保留的字符数。
	excerptLeading = 12
	// excerptLength 是摘要保留的最大字符数。
	excerptLength = 120
)

// Query 保存解析后的检索词，同一检索词的多种匹配方式满足其一；any 为真时检索词之间满足其一，否则同时满足。
type Query struct {
	terms [][]pattern
	any   bool
}

// pattern 表示在连续位置上逐个匹配的词元序列。
type pattern []matcher

// matcher 匹配单个位置上的词元，prefix 为真时按前缀匹配；alternatives 非空时匹配其中任一词元。
type matcher struct {
	lexeme       string
	prefix       bool
	alternatives []string
}

// Segment 表示摘要中的一段文字及其是否命中。
type Segment struct {
	Text  string
	Match bool
}

// ParseQuery 解析用户输入，按空白拆分检索词；没有可检索内容时返回 false。
func ParseQuery(input string) (Query, bool) {
	var query Query
	for _, field := range strings.FieldsFunc(input, unicode.IsSpace) {
		slots := tokenize(field, false)
		if len(slots) == 0 {
			continue
		}
		// 检索词末尾的字母或数字片段按前缀匹配，支持边输入边检索。
		literal := make(pattern, 0, len(slots))
		for index, item := range slots {
			literal = append(literal, matcher{lexeme: item.lexemes[0], prefix: item.word && index == len(slots)-1})
		}
		alternatives := []pattern{literal}
		if len(slots) == 1 && slots[0].word {
			for _, split := range splitPinyin(slots[0].lexemes[0]) {
				// 末个音节匹配以它开头的全部完整音节，拼音输入到一半时同样能命中。
				syllables := make(pattern, 0, len(split))
				for index, syllable := range split {
					item := matcher{lexeme: pinyinPrefix + syllable}
					if index == len(split)-1 {
						item.alternatives = syllableCompletions(syllable)
					}
					syllables = append(syllables, item)
				}
				alternatives = append(alternatives, syllables)
			}
		}
		query.terms = append(query.terms, alternatives)
	}
	return query, len(query.terms) > 0
}

// ParseKeywords 解析空白分隔的检索词，每个检索词按相邻位置完整匹配，检索词之间满足其一；没有可检索内容时返回 false。
func ParseKeywords(input string) (Query, bool) {
	query := Query{any: true}
	for _, field := range strings.FieldsFunc(input, unicode.IsSpace) {
		slots := tokenize(field, false)
		if len(slots) == 0 {
			continue
		}
		literal := pattern(arr.Map(slots, func(item slot) matcher { return matcher{lexeme: item.lexemes[0]} }))
		query.terms = append(query.terms, []pattern{literal})
	}
	return query, len(query.terms) > 0
}

// TSQuery 返回可直接转换为 tsquery 的条件文本。
func (q Query) TSQuery() string {
	terms := make([]string, 0, len(q.terms))
	for _, alternatives := range q.terms {
		options := make([]string, 0, len(alternatives))
		for _, item := range alternatives {
			positions := make([]string, 0, len(item))
			for _, position := range item {
				value := "'" + position.lexeme + "'"
				switch {
				case len(position.alternatives) == 1:
					value = "'" + position.alternatives[0] + "'"
				case len(position.alternatives) > 1:
					value = "('" + strings.Join(position.alternatives, "' | '") + "')"
				case position.prefix:
					value += ":*"
				}
				positions = append(positions, value)
			}
			options = append(options, "("+strings.Join(positions, " <-> ")+")")
		}
		terms = append(terms, "("+strings.Join(options, " | ")+")")
	}
	if q.any {
		return strings.Join(terms, " | ")
	}
	return strings.Join(terms, " & ")
}

// matches 判断连续位置是否依次满足词元序列。
func (p pattern) matches(slots []slot) bool {
	for index, position := range p {
		if !slices.ContainsFunc(slots[index].lexemes, func(lexeme string) bool {
			if len(position.alternatives) > 0 {
				return slices.Contains(position.alternatives, lexeme)
			}
			return lexeme == position.lexeme || position.prefix && strings.HasPrefix(lexeme, position.lexeme)
		}) {
			return false
		}
	}
	return true
}

// matchedRunes 标记原文中命中检索词的字符；文本不含命中时返回 false。
func (q Query) matchedRunes(text string) ([]bool, bool) {
	slots := tokenize(text, true)
	matched := make([]bool, utf8.RuneCountInString(text))
	found := false
	for _, alternatives := range q.terms {
		for _, item := range alternatives {
			for start := 0; start+len(item) <= len(slots); start++ {
				if !item.matches(slots[start : start+len(item)]) {
					continue
				}
				found = true
				for index := slots[start].start; index < slots[start+len(item)-1].end; index++ {
					matched[index] = true
				}
			}
		}
	}
	return matched, found
}

// Window 返回不超过 limit 个字符的原文片段；超长时从首个命中前约四分之一长度处开始截取，截断处以省略号标记。
func (q Query) Window(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	begin := 0
	if matched, found := q.matchedRunes(text); found {
		begin = max(0, min(slices.Index(matched, true)-limit/4, len(runes)-limit))
	}
	end := begin + limit
	window := string(runes[begin:end])
	if begin > 0 {
		window = "…" + window
	}
	if end < len(runes) {
		window += "…"
	}
	return window
}

// Excerpt 返回从首个命中附近开始的单行摘要；文本不含命中时返回 false。
func (q Query) Excerpt(text string) ([]Segment, bool) {
	matched, found := q.matchedRunes(text)
	if !found {
		return nil, false
	}
	runes := []rune(text)
	begin := max(0, slices.Index(matched, true)-excerptLeading)
	end := min(len(runes), begin+excerptLength)
	var segments []Segment
	appendText := func(value string, match bool) {
		if len(segments) > 0 && segments[len(segments)-1].Match == match {
			segments[len(segments)-1].Text += value
			return
		}
		segments = append(segments, Segment{Text: value, Match: match})
	}
	if begin > 0 {
		appendText("…", false)
	}
	for index := begin; index < end; {
		// 同一命中状态的连续字符合并为一段，换行等空白统一显示为空格。
		next := index
		for next < end && matched[next] == matched[index] {
			next++
		}
		appendText(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, string(runes[index:next])), matched[index])
		index = next
	}
	if end < len(runes) {
		appendText("…", false)
	}
	return segments, true
}
