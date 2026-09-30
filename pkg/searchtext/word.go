package searchtext

import (
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-ego/gse"
)

var (
	dictionaryOnce      sync.Once
	segmenter           gse.Segmenter
	dictionaryLoadError error
)

// dictionaryWord 表示分词得到的一个多字汉语词及其首字的字符序号。
type dictionaryWord struct {
	lexeme     string
	start, end int
}

// LoadDictionary 加载词典分词使用的简体词典与停用词，进程内只执行一次。
func LoadDictionary() error {
	dictionaryOnce.Do(func() {
		started := time.Now()
		segmenter.SkipLog = true
		if dictionaryLoadError = segmenter.LoadDictEmbed("zh_s"); dictionaryLoadError != nil {
			return
		}
		if dictionaryLoadError = segmenter.LoadStopEmbed(); dictionaryLoadError != nil {
			return
		}
		slog.Info("分词词典加载完成", "duration_ms", time.Since(started).Milliseconds())
	})
	return dictionaryLoadError
}

// dictionaryWords 切分原文中两个字以上的纯汉字词；三字以上的词同时给出词典中存在的子词，与搜索模式一致。
func dictionaryWords(text string) []dictionaryWord {
	if LoadDictionary() != nil {
		return nil
	}
	// 分词结果给出字节偏移，按字符序号对齐单字词元位置。
	runeIndex := make([]int, len(text)+1)
	count := 0
	for offset := range text {
		runeIndex[offset] = count
		count++
	}
	runeIndex[len(text)] = count
	var words []dictionaryWord
	for _, segment := range segmenter.Segment([]byte(text)) {
		word := text[segment.Start():segment.End()]
		if utf8.RuneCountInString(word) < 2 || strings.ContainsFunc(word, func(r rune) bool { return !unicode.Is(unicode.Han, r) }) {
			continue
		}
		start := runeIndex[segment.Start()]
		words = append(words, dictionaryWord{lexeme: word, start: start, end: runeIndex[segment.End()]})
		for _, sub := range segmenter.CutSearch(word, true) {
			if sub == word || utf8.RuneCountInString(sub) < 2 {
				continue
			}
			for offset := 0; ; {
				index := strings.Index(word[offset:], sub)
				if index < 0 {
					break
				}
				subStart := start + utf8.RuneCountInString(word[:offset+index])
				words = append(words, dictionaryWord{lexeme: sub, start: subStart, end: subStart + utf8.RuneCountInString(sub)})
				offset += index + len(sub)
			}
		}
	}
	return words
}

// WordVector 返回正文的 tsvector 字面量，包含单字、字母数字片段和搜索模式分词词元。
func WordVector(text string) string {
	positions := map[string][]int{}
	startPosition := map[int]int{}
	for position, item := range tokenize(text, false) {
		startPosition[item.start] = position + 1
		addPosition(positions, item.lexemes[0], position+1)
	}
	for _, word := range dictionaryWords(text) {
		if segmenter.IsStop(word.lexeme) {
			continue
		}
		if position, ok := startPosition[word.start]; ok {
			addPosition(positions, word.lexeme, position)
		}
	}
	return vectorLiteral(positions)
}

// WordQuery 把自然语言问句转成词元任一匹配的 tsquery 文本；去掉停用词后没有可检索内容时返回 false。
func WordQuery(input string) (string, bool) {
	slots := tokenize(input, false)
	words := dictionaryWords(input)
	covered := make([]bool, utf8.RuneCountInString(input))
	for _, word := range words {
		for index := word.start; index < word.end; index++ {
			covered[index] = true
		}
	}
	var terms []string
	seen := map[string]bool{}
	appendTerm := func(term string) {
		if !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	// 连续的字母数字片段按相邻短语匹配，未被分词覆盖且不是停用词的单字按单字匹配。
	for index := 0; index < len(slots); {
		if !slots[index].word {
			if !covered[slots[index].start] && !segmenter.IsStop(slots[index].lexemes[0]) {
				appendTerm("'" + slots[index].lexemes[0] + "'")
			}
			index++
			continue
		}
		next := index
		for next < len(slots) && slots[next].word {
			next++
		}
		phrase := make([]string, 0, next-index)
		for _, item := range slots[index:next] {
			phrase = append(phrase, "'"+item.lexemes[0]+"'")
		}
		if len(phrase) == 1 {
			appendTerm(phrase[0])
		} else {
			appendTerm("(" + strings.Join(phrase, " <-> ") + ")")
		}
		index = next
	}
	for _, word := range words {
		if !segmenter.IsStop(word.lexeme) {
			appendTerm("'" + word.lexeme + "'")
		}
	}
	return strings.Join(terms, " | "), len(terms) > 0
}
