//go:build server

package productdocs

import (
	"slices"
	"strings"
)

// searchLimit 是搜索结果的最大条数。
const searchLimit = 30

// snippetRadius 是摘要在命中位置前后保留的字符数。
const snippetRadius = 60

// SearchResult 是一条搜索结果；Anchor 非空时指向命中的标题。
type SearchResult struct {
	Page    *Page
	Title   string
	Anchor  string
	Snippet string
}

// Search 在指定语言的可见页面中按标题、小标题和正文匹配查询词，所有词都命中的页面才返回，标题命中排在前面。
func (s *Site) Search(locale, query string) []SearchResult {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		result SearchResult
		score  int
	}
	var matches []scored
	for _, page := range s.pages[locale] {
		if !s.visible(page) {
			continue
		}
		title := WithProduct(locale, page.Title)
		text := WithProduct(locale, page.Text)
		lowerTitle, lowerText := strings.ToLower(title), strings.ToLower(text)
		score, anchor := 0, ""
		for _, term := range terms {
			switch {
			case strings.Contains(lowerTitle, term):
				score += 100
			case strings.Contains(lowerText, term):
				score++
				// 命中小标题时加分并定位到第一个命中的小标题。
				for _, heading := range page.Headings {
					if strings.Contains(strings.ToLower(WithProduct(locale, heading.Text)), term) {
						score += 10
						if anchor == "" {
							anchor = heading.ID
						}
						break
					}
				}
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score < 0 {
			continue
		}
		matches = append(matches, scored{result: SearchResult{Page: page, Title: title, Anchor: anchor, Snippet: snippet(text, lowerText, terms[0])}, score: score})
	}
	slices.SortFunc(matches, func(a, b scored) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.result.Page.Slug, b.result.Page.Slug)
	})
	results := make([]SearchResult, 0, min(len(matches), searchLimit))
	for _, match := range matches[:min(len(matches), searchLimit)] {
		results = append(results, match.result)
	}
	return results
}

// snippet 返回正文中第一个查询词附近的片段；未命中正文时返回正文开头。
func snippet(text, lowerText, term string) string {
	runes := []rune(text)
	start := 0
	// 按小写正文中的字节位置换算命中处的字符位置。
	if index := strings.Index(lowerText, term); index >= 0 {
		start = len([]rune(lowerText[:index]))
	}
	from, to := max(start-snippetRadius, 0), min(start+snippetRadius*2, len(runes))
	result := string(runes[from:to])
	if from > 0 {
		result = "…" + result
	}
	if to < len(runes) {
		result += "…"
	}
	return result
}
