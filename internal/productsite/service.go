//go:build server

// Package productsite 在服务端根路径输出产品首页，与 /docs/ 下的产品文档共用页头与样式。
package productsite

import (
	"bytes"
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/productdocs"
)

//go:embed page.html
var pageTemplateSource string

// pageTemplate 渲染产品首页与 404 页面。
var pageTemplate = template.Must(template.New("page").Parse(pageTemplateSource))

// textKeys 是页面模板使用的界面文案。
var textKeys = map[string]i18n.Key{
	"language": i18n.SiteLanguage, "docs": i18n.SiteDocs, "openApp": i18n.SiteOpenApp, "signIn": i18n.SiteSignIn,
	"description": i18n.SiteDescription, "notFoundTitle": i18n.SiteNotFoundTitle, "notFoundBody": i18n.SiteNotFoundBody,
	"backHome": i18n.SiteBackHome, "heroEyebrow": i18n.SiteHeroEyebrow, "heroTitle": i18n.SiteHeroTitle,
	"heroBody": i18n.SiteHeroBody, "heroDeploy": i18n.SiteHeroDeploy, "demoCustomer": i18n.SiteDemoCustomer,
	"demoAgentName": i18n.SiteDemoAgentName, "demoAgentReply": i18n.SiteDemoAgentReply, "demoHandoff": i18n.SiteDemoHandoff,
	"demoMemberName": i18n.SiteDemoMemberName, "demoMemberReply": i18n.SiteDemoMemberReply,
	"featuresTitle": i18n.SiteFeaturesTitle, "featuresBody": i18n.SiteFeaturesBody,
	"platformsTitle": i18n.SitePlatformsTitle, "platformsBody": i18n.SitePlatformsBody, "platformWeb": i18n.SitePlatformWeb,
	"platformDesktop": i18n.SitePlatformDesktop, "platformMobile": i18n.SitePlatformMobile,
	"selfHostTitle": i18n.SiteSelfHostTitle, "selfHostBody": i18n.SiteSelfHostBody, "selfHostAction": i18n.SiteSelfHostAction,
	"closingTitle": i18n.SiteClosingTitle, "closingBody": i18n.SiteClosingBody,
}

// features 是首页能力区块的图标与文案，按展示顺序排列。
var features = []struct {
	icon        string
	title, body i18n.Key
}{
	{"channels", i18n.SiteChannelsTitle, i18n.SiteChannelsBody},
	{"agents", i18n.SiteAgentsTitle, i18n.SiteAgentsBody},
	{"handoff", i18n.SiteHandoffTitle, i18n.SiteHandoffBody},
	{"knowledge", i18n.SiteKnowledgeTitle, i18n.SiteKnowledgeBody},
	{"collaboration", i18n.SiteCollaborationTitle, i18n.SiteCollaborationBody},
	{"insights", i18n.SiteInsightsTitle, i18n.SiteInsightsBody},
}

// icons 是页面使用的线性图标路径，按名称引用。
var icons = map[string]template.HTML{
	"channels":      `<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`,
	"agents":        `<path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/><path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/>`,
	"handoff":       `<path d="m16 3 4 4-4 4"/><path d="M20 7H4"/><path d="m8 21-4-4 4-4"/><path d="M4 17h16"/>`,
	"knowledge":     `<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>`,
	"collaboration": `<path d="M14 9a2 2 0 0 1-2 2H6l-4 4V4a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2z"/><path d="M18 9h2a2 2 0 0 1 2 2v11l-4-4h-6a2 2 0 0 1-2-2v-1"/>`,
	"insights":      `<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="m19 9-5 5-4-4-3 3"/>`,
	"web":           `<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`,
	"desktop":       `<rect width="20" height="14" x="2" y="3" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/>`,
	"mobile":        `<rect width="14" height="20" x="5" y="2" rx="2"/><path d="M12 18h.01"/>`,
	"server":        `<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01"/><path d="M6 18h.01"/>`,
}

// Service 在根路径下按语言目录输出产品首页，其余路径输出 404 页面。
type Service struct {
	stylesheet string
}

// NewService 创建产品首页服务；stylesheet 为与文档站点共用的样式地址。
func NewService(stylesheet string) *Service {
	return &Service{stylesheet: stylesheet}
}

// ServeHTTP 处理产品首页请求：根路径按语言偏好跳转到对应语言的首页。
func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	requestPath := request.URL.Path
	if requestPath == "" || requestPath == "/" {
		http.Redirect(writer, request, homePath(productdocs.PreferredLocale(request)), http.StatusFound)
		return
	}
	locale := strings.Trim(requestPath, "/")
	if productdocs.LanguageTag(locale) == "" {
		s.write(writer, request, http.StatusNotFound, s.newView(productdocs.PreferredLocale(request), true))
		return
	}
	if requestPath != homePath(locale) {
		http.Redirect(writer, request, homePath(locale), http.StatusMovedPermanently)
		return
	}
	s.write(writer, request, http.StatusOK, s.newView(locale, false))
}

// homePath 返回指定语言目录的首页地址。
func homePath(locale string) string {
	return "/" + locale + "/"
}

// pageView 是页面模板的数据。
type pageView struct {
	Lang           string
	Product        string
	Copyright      string
	Stylesheet     string
	HomePath       string
	DocsPath       string
	DeploymentPath string
	AppPath        string
	Text           map[string]string
	Features       []featureView
	Languages      []languageView
	Icons          map[string]template.HTML
	Script         template.JS
	NotFound       bool
}

// featureView 是一个能力区块的模板数据。
type featureView struct {
	Icon  string
	Title string
	Body  string
}

// languageView 是语言切换项的模板数据。
type languageView struct {
	Label   string
	Lang    string
	Path    string
	Current bool
}

// newView 生成指定语言的页面数据；notFound 为真时生成 404 页面。
func (s *Service) newView(locale string, notFound bool) pageView {
	tag := productdocs.LanguageTag(locale)
	view := pageView{
		Lang: tag, Product: brand.Current().Name(tag), Stylesheet: s.stylesheet,
		HomePath: homePath(locale), DocsPath: productdocs.PagePath(locale, ""),
		DeploymentPath: productdocs.PagePath(locale, "deployment"), AppPath: domain.WebAppPath,
		Text: i18n.LocalizeMap(tag, textKeys), Icons: icons, Script: productdocs.AccountScript, NotFound: notFound,
	}
	// 版权主体来自构建品牌，产品名称与构建品牌一致时显示。
	if view.Product == brand.Build().Name(tag) {
		view.Copyright = brand.Current().Copyright
	}
	for _, feature := range features {
		title, _ := i18n.Localize(tag, feature.title)
		body, _ := i18n.Localize(tag, feature.body)
		view.Features = append(view.Features, featureView{Icon: feature.icon, Title: title, Body: body})
	}
	for _, candidate := range productdocs.Locales {
		view.Languages = append(view.Languages, languageView{
			Label: productdocs.LanguageLabel(candidate), Lang: productdocs.LanguageTag(candidate),
			Path: homePath(candidate), Current: candidate == locale,
		})
	}
	return view
}

// write 渲染页面模板并写入响应。
func (s *Service) write(writer http.ResponseWriter, request *http.Request, status int, view pageView) {
	var body bytes.Buffer
	if err := pageTemplate.Execute(&body, view); err != nil {
		slog.Error("渲染产品首页失败", "path", request.URL.Path, "error", err)
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
