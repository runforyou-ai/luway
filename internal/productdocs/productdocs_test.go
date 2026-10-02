//go:build server

package productdocs

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/runforyou-ai/luway/docs"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// allConditions 让声明了显示条件的页面全部可见。
var allConditions = Conditions{ConditionCommerce: true, ConditionInstanceLicense: true}

// TestContent 校验内置文档：中英文页面一一对应、frontmatter 完整、显示条件合法、站内链接与锚点存在、导航覆盖全部页面。
func TestContent(t *testing.T) {
	site, err := Load(docs.Content, allConditions)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range Locales {
		for slug, page := range site.pages[locale] {
			for _, other := range Locales {
				if _, ok := site.pages[other][slug]; !ok {
					t.Errorf("%s/%s 缺少 %s 版本", locale, slug, other)
				}
			}
			if page.Title == "" {
				t.Errorf("%s/%s 缺少标题", locale, slug)
			}
			if strings.Contains(page.Title, productPlaceholder) {
				t.Errorf("%s/%s 的标题不得包含产品名称", locale, slug)
			}
			for _, condition := range page.Requires {
				if _, ok := allConditions[condition]; !ok {
					t.Errorf("%s/%s 使用了未定义的显示条件 %q", locale, slug, condition)
				}
			}
			if english := site.pages["en"][slug]; english != nil && !slices.Equal(english.Requires, page.Requires) {
				t.Errorf("%s/%s 的显示条件与英文版本不一致", locale, slug)
			}
			for _, link := range page.Links {
				checkLink(t, site, page, link)
			}
		}
	}
	checkNavigation(t, site)
}

// checkLink 校验站内链接指向存在的页面与标题锚点。
func checkLink(t *testing.T, site *Site, page *Page, link string) {
	t.Helper()
	target, anchor, _ := strings.Cut(link, "#")
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return
	}
	targetPage := page
	if target != "" {
		rest, ok := strings.CutPrefix(target, Prefix)
		if !ok || !strings.HasSuffix(rest, "/") {
			t.Errorf("%s 的站内链接 %q 必须写成以 %s 开头、以斜杠结尾的路径", page.Path(), link, Prefix)
			return
		}
		locale, slug, _ := strings.Cut(strings.TrimSuffix(rest, "/"), "/")
		targetPage = site.pages[locale][slug]
		if targetPage == nil {
			t.Errorf("%s 的链接 %q 指向不存在的页面", page.Path(), link)
			return
		}
	}
	if anchor != "" && !slices.ContainsFunc(targetPage.Headings, func(heading Heading) bool { return heading.ID == anchor }) {
		t.Errorf("%s 的链接 %q 指向不存在的标题", page.Path(), link)
	}
}

// checkNavigation 校验导航标题有中英文、栏目首页存在，且每个页面恰好出现在一个栏目首页或分组中。
func checkNavigation(t *testing.T, site *Site) {
	t.Helper()
	placed := map[string]int{"": 1}
	for _, section := range site.nav.Sections {
		for _, locale := range Locales {
			if section.Title[locale] == "" {
				t.Errorf("栏目 %s 缺少 %s 标题", section.Path, locale)
			}
		}
		if _, ok := site.pages[Locales[0]][section.Path]; !ok {
			t.Errorf("栏目 %s 缺少首页", section.Path)
		}
		placed[section.Path]++
		for _, group := range section.Groups {
			for _, locale := range Locales {
				if group.Title[locale] == "" {
					t.Errorf("分组 %s 缺少 %s 标题", group.Dir, locale)
				}
			}
			for slug := range site.pages[Locales[0]] {
				if slug == group.Dir || strings.HasPrefix(slug, group.Dir+"/") {
					placed[slug]++
				}
			}
		}
	}
	for slug := range site.pages[Locales[0]] {
		if placed[slug] != 1 {
			t.Errorf("页面 %q 在导航中出现 %d 次", slug, placed[slug])
		}
	}
}

// testSite 返回用于渲染与路由测试的最小文档。
func testSite(t *testing.T, conditions Conditions) *Site {
	t.Helper()
	content := fstest.MapFS{
		"nav.yaml": {Data: []byte(`sections:
  - path: guide
    title: { zh-cn: 使用手册, en: User guide }
    groups:
      - { dir: guide/basics, title: { zh-cn: 入门, en: Basics } }
`)},
		"zh-cn/index.md":                 {Data: []byte("---\ntitle: 欢迎\n---\n\n欢迎使用 {{product}}。\n")},
		"en/index.md":                    {Data: []byte("---\ntitle: Welcome\n---\n\nWelcome to {{product}}.\n")},
		"zh-cn/guide/index.md":           {Data: []byte("---\ntitle: 概览\n---\n\n## 快速 开始\n\n## 快速 开始\n\n> [!NOTE]\n> 提示内容。\n\n```yaml\nkey: value\n```\n")},
		"en/guide/index.md":              {Data: []byte("---\ntitle: Overview\n---\n\n## Quick start\n")},
		"zh-cn/guide/basics/billing.md":  {Data: []byte("---\ntitle: 套餐\norder: 2\nrequires: [commerce]\n---\n\n购买套餐与席位。\n")},
		"en/guide/basics/billing.md":     {Data: []byte("---\ntitle: Plans\norder: 2\nrequires: [commerce]\n---\n\nBuy plans and seats.\n")},
		"zh-cn/guide/basics/concepts.md": {Data: []byte("---\ntitle: 基本概念\norder: 1\n---\n\n## 工作区\n\n工作区隔离成员与数据。\n")},
		"en/guide/basics/concepts.md":    {Data: []byte("---\ntitle: Key concepts\norder: 1\n---\n\n## Workspaces\n")},
	}
	site, err := Load(content, conditions)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

// TestRender 校验中文标题编号、重复标题去重、提示块与代码高亮的输出。
func TestRender(t *testing.T) {
	site := testSite(t, nil)
	page, ok := site.Page("zh-cn", "guide")
	if !ok {
		t.Fatal("guide page missing")
	}
	ids := []string{page.Headings[0].ID, page.Headings[1].ID}
	if !slices.Equal(ids, []string{"快速-开始", "快速-开始-1"}) {
		t.Fatalf("heading ids = %v", ids)
	}
	for _, want := range []string{`id="快速-开始"`, `<blockquote class="docs-callout docs-callout-note"><p class="docs-callout-title">注意</p>`, `class="chroma"`} {
		if !strings.Contains(page.HTML, want) {
			t.Errorf("rendered html missing %q:\n%s", want, page.HTML)
		}
	}
}

// TestConditions 校验显示条件不成立的页面在导航、搜索与正文片段中均不可见。
func TestConditions(t *testing.T) {
	hidden := testSite(t, nil)
	if _, ok := hidden.Fragment("zh-cn", "guide/basics/billing"); ok {
		t.Fatal("billing page must be hidden without commerce")
	}
	if results := hidden.Search("zh-cn", "套餐"); len(results) != 0 {
		t.Fatalf("hidden page found by search: %v", results)
	}
	if pages := hidden.Navigation("zh-cn")[0].Groups[0].Pages; len(pages) != 1 {
		t.Fatalf("navigation pages = %d", len(pages))
	}
	visible := testSite(t, Conditions{ConditionCommerce: true})
	if pages := visible.Navigation("zh-cn")[0].Groups[0].Pages; len(pages) != 2 || pages[0].Slug != "guide/basics/concepts" {
		t.Fatalf("navigation order = %v", pages)
	}
}

// TestFragmentProductName 校验正文片段的产品名称按语言替换为当前部署品牌。
func TestFragmentProductName(t *testing.T) {
	site := testSite(t, nil)
	fragment, ok := site.Fragment("zh-cn", "")
	if !ok {
		t.Fatal("home page missing")
	}
	if want := brand.Current().Name("zh-CN"); !strings.Contains(fragment.HTML, want) || strings.Contains(fragment.HTML, productPlaceholder) {
		t.Fatalf("fragment html = %q, want product %q", fragment.HTML, want)
	}
	if fragment.Path != "/docs/zh-cn/" {
		t.Fatalf("fragment path = %q", fragment.Path)
	}
}

// TestService 校验文档路由：语言跳转、补全斜杠、页面输出、搜索、404 与方法限制。
func TestService(t *testing.T) {
	service, err := NewService(testSite(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method, target, language string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://example.com"+target, nil)
		request.Header.Set("Accept-Language", language)
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/docs")
		recorder := httptest.NewRecorder()
		service.ServeHTTP(recorder, request)
		return recorder
	}
	redirects := []struct{ target, language, location string }{
		{"/docs", "", "/docs/"},
		{"/docs/", "zh-CN,zh;q=0.9", "/docs/zh-cn/"},
		{"/docs/", "en-US", "/docs/en/"},
		{"/docs/en/guide", "", "/docs/en/guide/"},
		{"/docs/en/search?q=x", "", "/docs/en/search/?q=x"},
	}
	for _, redirect := range redirects {
		response := serve(http.MethodGet, redirect.target, redirect.language)
		if location := response.Header().Get("Location"); location != redirect.location {
			t.Errorf("%s redirect = %d %q, want %q", redirect.target, response.Code, location, redirect.location)
		}
	}
	page := serve(http.MethodGet, "/docs/zh-cn/guide/basics/concepts/", "")
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "<h1>基本概念</h1>") || !strings.Contains(body, `href="#工作区"`) || !strings.Contains(body, `aria-current="page"`) {
		t.Fatalf("page response = %d\n%s", page.Code, body)
	}
	if !strings.Contains(body, `href="/docs/en/guide/basics/concepts/"`) {
		t.Fatalf("language switch missing:\n%s", body)
	}
	search := serve(http.MethodGet, "/docs/zh-cn/search/?q=工作区", "")
	// html/template 对链接中的中文锚点做百分号编码。
	if !strings.Contains(search.Body.String(), `href="/docs/zh-cn/guide/basics/concepts/#%e5%b7%a5%e4%bd%9c%e5%8c%ba"`) {
		t.Fatalf("search response:\n%s", search.Body.String())
	}
	for _, target := range []string{"/docs/zh-cn/guide/basics/billing/", "/docs/zh-cn/missing/", "/docs/fr/", "/docsfoo"} {
		if response := serve(http.MethodGet, target, ""); response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d", target, response.Code)
		}
	}
	if response := serve(http.MethodPost, "/docs/zh-cn/", ""); response.Code != http.StatusMethodNotAllowed {
		t.Errorf("post status = %d", response.Code)
	}
}
