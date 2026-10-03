//go:build server

// Package productdocs 渲染内置的产品文档，在 /docs/ 下输出完整页面并为应用内帮助提供正文片段。
package productdocs

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// Locales 是文档提供的语言目录，首个为默认语言。
var Locales = []string{"zh-cn", "en"}

// brandLocales 把文档语言目录映射到品牌名称使用的界面语言标签。
var brandLocales = map[string]string{"zh-cn": "zh-CN", "en": "en-US"}

// LanguageTag 返回语言目录对应的界面语言标签。
func LanguageTag(locale string) string {
	return brandLocales[locale]
}

// productPlaceholder 是正文中的产品名称占位符，输出时替换为当前平台的品牌名称。
const productPlaceholder = "{{product}}"

// ConditionCommerce 表示平台已配对商业服务。
const ConditionCommerce = "commerce"

// Conditions 是当前平台成立的显示条件，页面声明的条件全部成立时才可见。
type Conditions map[string]bool

// Page 是一页已渲染的文档；Slug 为语言目录下的路径，首页为空字符串。
type Page struct {
	Locale   string
	Slug     string
	Title    string
	Order    int
	Requires []string
	HTML     string
	Text     string
	Headings []Heading
	Links    []string
}

// Path 返回页面的访问路径。
func (p *Page) Path() string {
	return PagePath(p.Locale, p.Slug)
}

// PagePath 返回指定语言与页面路径的访问路径，以斜杠结尾。
func PagePath(locale, slug string) string {
	if slug == "" {
		return Prefix + locale + "/"
	}
	return Prefix + locale + "/" + slug + "/"
}

// navConfig 是 nav.yaml 中的栏目与分组配置，标题以语言目录为键。
type navConfig struct {
	Sections []struct {
		Path   string            `yaml:"path"`
		Title  map[string]string `yaml:"title"`
		Groups []struct {
			Dir   string            `yaml:"dir"`
			Title map[string]string `yaml:"title"`
		} `yaml:"groups"`
	} `yaml:"sections"`
}

// NavSection 是侧边栏中的一个栏目：栏目首页与其下的分组。
type NavSection struct {
	Title  string
	Index  *Page
	Groups []NavGroup
}

// NavGroup 是栏目中的一个分组及其页面。
type NavGroup struct {
	Title string
	Pages []*Page
}

// Site 持有全部语言的已渲染页面与导航，只读并发安全。
type Site struct {
	pages      map[string]map[string]*Page
	nav        navConfig
	conditions Conditions
}

// Load 读取并渲染文档源文件；content 根目录包含 nav.yaml 与各语言目录，conditions 为当前平台成立的显示条件。
func Load(content fs.FS, conditions Conditions) (*Site, error) {
	site := &Site{pages: map[string]map[string]*Page{}, conditions: conditions}
	raw, err := fs.ReadFile(content, "nav.yaml")
	if err != nil {
		return nil, fmt.Errorf("read docs navigation: %w", err)
	}
	if err := yaml.UnmarshalWithOptions(raw, &site.nav, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse docs navigation: %w", err)
	}
	for _, locale := range Locales {
		site.pages[locale] = map[string]*Page{}
		err := fs.WalkDir(content, locale, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || path.Ext(name) != ".md" {
				return err
			}
			source, err := fs.ReadFile(content, name)
			if err != nil {
				return err
			}
			result, err := render(locale, source)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			// 语言目录下的相对路径去掉扩展名，index 页面使用所在目录。
			slug := strings.TrimSuffix(strings.TrimPrefix(name, locale+"/"), ".md")
			if slug == "index" {
				slug = ""
			}
			slug = strings.TrimSuffix(slug, "/index")
			site.pages[locale][slug] = &Page{
				Locale: locale, Slug: slug, Title: result.meta.Title, Order: result.meta.Order, Requires: result.meta.Requires,
				HTML: result.html, Text: result.text, Headings: result.headings, Links: result.links,
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("load %s docs: %w", locale, err)
		}
	}
	if len(site.pages[Locales[0]]) == 0 {
		return nil, errors.New("load docs: no pages")
	}
	return site, nil
}

// Page 返回当前平台可见的页面。
func (s *Site) Page(locale, slug string) (*Page, bool) {
	page, ok := s.pages[locale][slug]
	if !ok || !s.visible(page) {
		return nil, false
	}
	return page, true
}

// visible 报告页面声明的显示条件是否全部成立。
func (s *Site) visible(page *Page) bool {
	for _, condition := range page.Requires {
		if !s.conditions[condition] {
			return false
		}
	}
	return true
}

// Navigation 返回指定语言的侧边栏，只包含当前平台可见的页面。
func (s *Site) Navigation(locale string) []NavSection {
	sections := make([]NavSection, 0, len(s.nav.Sections))
	for _, config := range s.nav.Sections {
		section := NavSection{Title: config.Title[locale]}
		section.Index, _ = s.Page(locale, config.Path)
		for _, groupConfig := range config.Groups {
			group := NavGroup{Title: groupConfig.Title[locale]}
			for slug, page := range s.pages[locale] {
				if (slug == groupConfig.Dir || strings.HasPrefix(slug, groupConfig.Dir+"/")) && s.visible(page) {
					group.Pages = append(group.Pages, page)
				}
			}
			// 分组内按 order 排序，order 相同时按路径排序。
			slices.SortFunc(group.Pages, func(a, b *Page) int {
				if a.Order != b.Order {
					return a.Order - b.Order
				}
				return strings.Compare(a.Slug, b.Slug)
			})
			if len(group.Pages) > 0 {
				section.Groups = append(section.Groups, group)
			}
		}
		if section.Index != nil || len(section.Groups) > 0 {
			sections = append(sections, section)
		}
	}
	return sections
}

// ProductName 返回指定文档语言的当前部署品牌名称。
func ProductName(locale string) string {
	return brand.Current().Name(brandLocales[locale])
}

// WithProduct 把文本中的产品名称占位符替换为当前平台的品牌名称。
func WithProduct(locale, value string) string {
	return strings.ReplaceAll(value, productPlaceholder, ProductName(locale))
}

// htmlWithProduct 把 HTML 中的产品名称占位符替换为转义后的当前部署品牌名称。
func htmlWithProduct(locale, value string) string {
	return strings.ReplaceAll(value, productPlaceholder, html.EscapeString(ProductName(locale)))
}

// Fragment 是应用内帮助显示的页面正文。
type Fragment struct {
	Title string
	HTML  string
	Path  string
}

// Fragment 返回当前平台可见页面的标题与正文，产品名称已替换。
func (s *Site) Fragment(locale, slug string) (Fragment, bool) {
	page, ok := s.Page(locale, slug)
	if !ok {
		return Fragment{}, false
	}
	return Fragment{Title: WithProduct(locale, page.Title), HTML: htmlWithProduct(locale, page.HTML), Path: page.Path()}, true
}
