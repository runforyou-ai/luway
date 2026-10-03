//go:build server

package productdocs

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/webasset"
)

// Prefix 是产品文档的访问路径前缀。
const Prefix = "/docs/"

// assetsPrefix 是文档样式与字体在 Prefix 下的路径。
const assetsPrefix = "_assets/"

// files 包含前端构建到 dist/site 的文档样式与字体；未构建时 dist 下只有占位文件。
//
//go:embed all:dist
var files embed.FS

//go:embed page.html
var pageTemplateSource string

//go:embed account.js
var accountScriptSource string

// AccountScript 是文档页与产品首页共用的页头账号脚本：已登录时显示当前账号，否则保留登录入口。
var AccountScript = template.JS(accountScriptSource)

// pageTemplate 渲染文档页面、搜索结果页与 404 页面。
var pageTemplate = template.Must(template.New("page").Parse(pageTemplateSource))

// languageLabels 是语言切换中各语言的名称。
var languageLabels = map[string]string{"zh-cn": "简体中文", "en": "English"}

// LanguageLabel 返回语言切换中该语言目录的名称。
func LanguageLabel(locale string) string {
	return languageLabels[locale]
}

// assetTypes 是文档样式资源的响应类型。
var assetTypes = map[string]string{".css": "text/css; charset=utf-8", ".woff2": "font/woff2"}

// Service 在 /docs 下输出文档页面、搜索结果与样式资源。
type Service struct {
	site         *Site
	assets       map[string]webasset.Asset
	assetVersion string
}

// NewService 创建文档页面服务；文档样式未构建时页面不带样式输出。
func NewService(site *Site) (*Service, error) {
	service := &Service{site: site, assets: map[string]webasset.Asset{}}
	err := fs.WalkDir(files, "dist", func(name string, entry fs.DirEntry, err error) error {
		contentType, ok := assetTypes[path.Ext(name)]
		if err != nil || entry.IsDir() || !ok {
			return err
		}
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		service.assets[strings.TrimPrefix(name, "dist/site/")] = webasset.New(contentType, raw)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load docs assets: %w", err)
	}
	service.assetVersion = stylesheetVersion()
	if service.assetVersion == "" {
		slog.Warn("服务端未内置产品文档样式")
	}
	return service, nil
}

// stylesheetVersion 返回内置样式内容摘要的前缀，样式未构建时为空。
var stylesheetVersion = sync.OnceValue(func() string {
	raw, err := fs.ReadFile(files, "dist/site/site.css")
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:8])
})

// StylesheetPath 返回文档站点与产品首页共用的样式地址，携带当前样式版本。
func StylesheetPath() string {
	return Prefix + assetsPrefix + "site.css?v=" + stylesheetVersion()
}

// ServeHTTP 处理去掉 /docs 前缀后的请求路径。
func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	requestPath := request.URL.Path
	switch {
	case requestPath == "":
		http.Redirect(writer, request, Prefix, http.StatusMovedPermanently)
		return
	case requestPath == "/":
		http.Redirect(writer, request, Prefix+PreferredLocale(request)+"/", http.StatusFound)
		return
	case !strings.HasPrefix(requestPath, "/"):
		s.writeNotFound(writer, request, PreferredLocale(request))
		return
	}
	if name, ok := strings.CutPrefix(requestPath, "/"+assetsPrefix); ok {
		asset, found := s.assets[name]
		if !found {
			http.NotFound(writer, request)
			return
		}
		// 字体文件名含内容哈希，样式地址携带当前版本时同样长期缓存。
		cacheControl := webasset.RevalidateCache
		if strings.HasPrefix(name, "fonts/") || (name == "site.css" && request.URL.Query().Get("v") == s.assetVersion) {
			cacheControl = webasset.ImmutableCache
		}
		asset.Serve(writer, request, cacheControl)
		return
	}
	locale, slug, _ := strings.Cut(strings.TrimPrefix(requestPath, "/"), "/")
	if _, ok := languageLabels[locale]; !ok {
		s.writeNotFound(writer, request, PreferredLocale(request))
		return
	}
	if !strings.HasSuffix(requestPath, "/") {
		target := Prefix + strings.TrimPrefix(requestPath, "/") + "/"
		if request.URL.RawQuery != "" {
			target += "?" + request.URL.RawQuery
		}
		http.Redirect(writer, request, target, http.StatusMovedPermanently)
		return
	}
	slug = strings.TrimSuffix(slug, "/")
	if slug == "search" {
		s.writeSearch(writer, request, locale)
		return
	}
	page, ok := s.site.Page(locale, slug)
	if !ok {
		s.writeNotFound(writer, request, locale)
		return
	}
	view := s.newView(locale, slug)
	view.Title = WithProduct(locale, page.Title)
	view.HTML = template.HTML(htmlWithProduct(locale, page.HTML))
	for _, heading := range page.Headings {
		if heading.Level == 2 || heading.Level == 3 {
			view.TOC = append(view.TOC, tocView{Level: heading.Level, ID: heading.ID, Text: WithProduct(locale, heading.Text)})
		}
	}
	s.write(writer, request, http.StatusOK, view)
}

// PreferredLocale 按请求的语言偏好选择站点语言目录，中文偏好使用中文，其余使用英文。
func PreferredLocale(request *http.Request) string {
	for part := range strings.SplitSeq(request.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.Split(part, ";")[0]))
		if tag == "" || tag == "*" {
			continue
		}
		if strings.HasPrefix(tag, "zh") {
			return "zh-cn"
		}
		return "en"
	}
	return Locales[0]
}

// pageView 是页面模板的数据。
type pageView struct {
	Lang         string
	Locale       string
	SiteTitle    string
	Title        string
	HTML         template.HTML
	HomePath     string
	SearchPath   string
	SiteHomePath string
	DownloadPath string
	AppPath      string
	AssetPath    string
	Nav          []navSectionView
	TOC          []tocView
	Languages    []languageView
	Labels       map[string]string
	Search       *searchView
	NotFound     bool
	AssetVersion string
	Script       template.JS
}

// navSectionView 是侧边栏栏目的模板数据。
type navSectionView struct {
	Title  string
	Index  *navLinkView
	Groups []navGroupView
}

// navGroupView 是侧边栏分组的模板数据。
type navGroupView struct {
	Title string
	Pages []navLinkView
	Open  bool
}

// navLinkView 是侧边栏链接的模板数据。
type navLinkView struct {
	Title   string
	Path    string
	Current bool
}

// tocView 是本页目录项的模板数据。
type tocView struct {
	Level int
	ID    string
	Text  string
}

// languageView 是语言切换项的模板数据。
type languageView struct {
	Label   string
	Lang    string
	Path    string
	Current bool
}

// searchView 是搜索结果页的模板数据。
type searchView struct {
	Query   string
	Heading string
	Empty   string
	Results []searchResultView
}

// searchResultView 是一条搜索结果的模板数据。
type searchResultView struct {
	Title   string
	Path    string
	Snippet string
}

// newView 生成页面共用的站点标题、导航、语言切换与界面文案；slug 为当前页面，用于高亮导航和切换语言。
func (s *Service) newView(locale, slug string) pageView {
	acceptLanguage := brandLocales[locale]
	siteTitle, lang := i18n.Localize(acceptLanguage, i18n.DocsSiteTitle)
	view := pageView{
		Lang: lang, Locale: locale, SiteTitle: siteTitle,
		HomePath: PagePath(locale, ""), SearchPath: PagePath(locale, "search"), AssetPath: Prefix + assetsPrefix,
		SiteHomePath: SitePath(locale, ""), DownloadPath: SitePath(locale, SiteDownloadPage), AppPath: domain.WebAppPath,
		AssetVersion: s.assetVersion, Script: AccountScript,
		Labels: i18n.LocalizeMap(acceptLanguage, map[string]i18n.Key{
			"search": i18n.DocsSearch, "onThisPage": i18n.DocsOnThisPage, "menu": i18n.DocsMenu, "language": i18n.SiteLanguage,
			"home": i18n.SiteHome, "download": i18n.SiteDownload, "openApp": i18n.SiteOpenApp, "signIn": i18n.SiteSignIn,
		}),
	}
	for _, section := range s.site.Navigation(locale) {
		sectionView := navSectionView{Title: section.Title}
		if section.Index != nil {
			sectionView.Index = &navLinkView{Title: WithProduct(locale, section.Index.Title), Path: section.Index.Path(), Current: section.Index.Slug == slug}
		}
		for _, group := range section.Groups {
			groupView := navGroupView{Title: group.Title}
			for _, page := range group.Pages {
				current := page.Slug == slug
				groupView.Open = groupView.Open || current
				groupView.Pages = append(groupView.Pages, navLinkView{Title: WithProduct(locale, page.Title), Path: page.Path(), Current: current})
			}
			sectionView.Groups = append(sectionView.Groups, groupView)
		}
		view.Nav = append(view.Nav, sectionView)
	}
	for _, candidate := range Locales {
		// 当前页面在其他语言中不可见时切换到该语言的首页。
		target := PagePath(candidate, "")
		if _, ok := s.site.Page(candidate, slug); ok || slug == "search" {
			target = PagePath(candidate, slug)
		}
		view.Languages = append(view.Languages, languageView{Label: languageLabels[candidate], Lang: brandLocales[candidate], Path: target, Current: candidate == locale})
	}
	return view
}

// writeSearch 输出搜索结果页。
func (s *Service) writeSearch(writer http.ResponseWriter, request *http.Request, locale string) {
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	view := s.newView(locale, "search")
	acceptLanguage := brandLocales[locale]
	view.Title, _ = i18n.Localize(acceptLanguage, i18n.DocsSearch)
	search := &searchView{Query: query}
	if query == "" {
		search.Empty, _ = i18n.Localize(acceptLanguage, i18n.DocsSearchPrompt)
	} else {
		search.Heading = i18n.LocalizeTemplate(acceptLanguage, i18n.DocsSearchResults, map[string]any{"Query": query})
		for _, result := range s.site.Search(locale, query) {
			target := result.Page.Path()
			if result.Anchor != "" {
				target += "#" + result.Anchor
			}
			search.Results = append(search.Results, searchResultView{Title: result.Title, Path: target, Snippet: result.Snippet})
		}
		if len(search.Results) == 0 {
			search.Empty, _ = i18n.Localize(acceptLanguage, i18n.DocsSearchEmpty)
		}
	}
	view.Search = search
	s.write(writer, request, http.StatusOK, view)
}

// writeNotFound 输出 404 页面。
func (s *Service) writeNotFound(writer http.ResponseWriter, request *http.Request, locale string) {
	view := s.newView(locale, "")
	acceptLanguage := brandLocales[locale]
	view.Title, _ = i18n.Localize(acceptLanguage, i18n.SiteNotFoundTitle)
	body, _ := i18n.Localize(acceptLanguage, i18n.DocsNotFoundBody)
	home, _ := i18n.Localize(acceptLanguage, i18n.DocsBackHome)
	view.HTML = template.HTML("<p>" + template.HTMLEscapeString(body) + "</p><p><a href=\"" + view.HomePath + "\">" + template.HTMLEscapeString(home) + "</a></p>")
	view.NotFound = true
	s.write(writer, request, http.StatusNotFound, view)
}

// write 渲染页面模板并写入响应。
func (s *Service) write(writer http.ResponseWriter, request *http.Request, status int, view pageView) {
	var body bytes.Buffer
	if err := pageTemplate.Execute(&body, view); err != nil {
		slog.Error("渲染产品文档页面失败", "path", request.URL.Path, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(body.Bytes())
	}
}
