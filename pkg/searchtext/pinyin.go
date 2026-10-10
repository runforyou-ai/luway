package searchtext

import (
	"slices"
	"strings"
	"sync"

	"github.com/mozillazg/go-pinyin"
)

const (
	// maxPinyinQueryRunes 是按拼音解析的检索词最大字母数。
	maxPinyinQueryRunes = 36
	// maxPinyinSplits 是单个检索词保留的拼音切分方案数量。
	maxPinyinSplits = 8
	// maxSyllableRunes 是最长拼音音节的字母数。
	maxSyllableRunes = 6
)

// pinyinArgs 输出不带声调的全部读音，ü 写作 v。
var pinyinArgs = func() pinyin.Args {
	args := pinyin.NewArgs()
	args.Heteronym = true
	return args
}()

// syllables 返回拼音字典中含元音的无声调音节，以及这些音节的真前缀；n、ng、m 等叹词读音不参与检索词切分。
var syllables = sync.OnceValues(func() (map[string]bool, map[string]bool) {
	full, prefixes := map[string]bool{}, map[string]bool{}
	for code := range pinyin.PinyinDict {
		for _, reading := range pinyin.SinglePinyin(rune(code), pinyinArgs) {
			if !isASCIILetters(reading) || !strings.ContainsAny(reading, "aeiouv") {
				continue
			}
			full[reading] = true
			for end := 1; end < len(reading); end++ {
				prefixes[reading[:end]] = true
			}
		}
	}
	return full, prefixes
})

// syllableCompletions 返回以指定字母开头的全部完整音节对应的读音词元，按字母序排列。
func syllableCompletions(prefix string) []string {
	full, _ := syllables()
	var lexemes []string
	for syllable := range full {
		if strings.HasPrefix(syllable, prefix) {
			lexemes = append(lexemes, pinyinPrefix+syllable)
		}
	}
	slices.Sort(lexemes)
	return lexemes
}

// pinyinLexemes 返回汉字全部读音对应的读音词元。
func pinyinLexemes(r rune) []string {
	readings := pinyin.SinglePinyin(r, pinyinArgs)
	lexemes := make([]string, 0, len(readings))
	for _, reading := range readings {
		lexeme := pinyinPrefix + reading
		if isASCIILetters(reading) && !slices.Contains(lexemes, lexeme) {
			lexemes = append(lexemes, lexeme)
		}
	}
	return lexemes
}

// splitPinyin 返回字母检索词可能的拼音音节切分，长音节优先；a、o、e 开头的音节只作为首个音节，与拼音书写中这类音节前需加隔音符号的规则一致；不存在完整切分时，末段取某个音节的开头。
func splitPinyin(word string) [][]string {
	if len(word) > maxPinyinQueryRunes || !isASCIILetters(word) {
		return nil
	}
	full, prefixes := syllables()
	var splits [][]string
	var walk func(rest string, current []string, partial bool)
	walk = func(rest string, current []string, partial bool) {
		if len(splits) >= maxPinyinSplits {
			return
		}
		if rest == "" {
			if !partial {
				splits = append(splits, slices.Clone(current))
			}
			return
		}
		if len(current) > 0 && strings.ContainsRune("aoe", rune(rest[0])) {
			return
		}
		for end := min(len(rest), maxSyllableRunes); end >= 1; end-- {
			if full[rest[:end]] {
				walk(rest[end:], append(current, rest[:end]), partial)
			}
		}
		if partial && prefixes[rest] && !full[rest] {
			splits = append(splits, append(slices.Clone(current), rest))
		}
	}
	walk(word, nil, false)
	if len(splits) == 0 {
		walk(word, nil, true)
	}
	return splits
}

// isASCIILetters 判断文本是否只由小写英文字母组成。
func isASCIILetters(text string) bool {
	if text == "" {
		return false
	}
	for index := range len(text) {
		if text[index] < 'a' || text[index] > 'z' {
			return false
		}
	}
	return true
}
