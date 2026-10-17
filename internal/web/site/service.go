//go:build server

// Package site 在服务端根路径输出产品首页与客户端下载页，并按能力开关裁剪联系、更新说明、授权管理与法律等页面；与 /docs/ 共用内联样式与脚本。
package site

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/release"
	releaseclients "github.com/runforyou-ai/luway/internal/release/clients"
	"github.com/runforyou-ai/luway/internal/web/assets"
	"github.com/runforyou-ai/luway/internal/web/docs"
)

// pageTemplateSource 是产品站页面模板。
//
//go:embed page.html
var pageTemplateSource string

// ThemeStyle 是内联输出的站点样式。
var ThemeStyle = assets.Style

// ThemeScript 是内联输出的站点脚本。
var ThemeScript = assets.Script

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
	"selfHostAction": i18n.SiteSelfHostAction,
	"closingTitle":   i18n.SiteClosingTitle, "closingBody": i18n.SiteClosingBody,
	"download": i18n.SiteDownload, "downloadTitle": i18n.SiteDownloadTitle, "downloadBody": i18n.SiteDownloadBody,
	"downloadCurrent": i18n.SiteDownloadCurrent, "downloadWeb": i18n.SiteDownloadWeb,
	"executorTitle": i18n.SiteExecutorTitle, "executorBody": i18n.SiteExecutorBody, "executorDocs": i18n.SiteExecutorDocs,
	"pricing": i18n.SitePricing, "pricingTitle": i18n.SitePricingTitle, "pricingBody": i18n.SitePricingBody, "pricingSignUp": i18n.SitePricingSignUp,
	"menu": i18n.SiteMenu, "learnMore": i18n.SiteLearnMore, "releaseNotes": i18n.SiteReleaseNotes,
	"support": i18n.SiteSupport, "legal": i18n.SiteLegal, "contact": i18n.SiteContact,
	"licenseManager": i18n.SiteLicenseManager, "privacy": i18n.SitePrivacy,
	"licenseAgreement": i18n.SiteLicenseAgreement, "terms": i18n.SiteTerms,
	"scene1Title": i18n.SiteScene1Title, "scene1Body": i18n.SiteScene1Body,
	"scene2Title": i18n.SiteScene2Title, "scene2Body": i18n.SiteScene2Body,
	"scene3Title": i18n.SiteScene3Title, "scene3Body": i18n.SiteScene3Body,
	"feat1Title": i18n.SiteFeat1Title, "feat1Body": i18n.SiteFeat1Body,
	"feat2Title": i18n.SiteFeat2Title, "feat2Body": i18n.SiteFeat2Body,
	"feat3Title": i18n.SiteFeat3Title, "feat3Body": i18n.SiteFeat3Body,
	"feat4Title": i18n.SiteFeat4Title, "feat4Body": i18n.SiteFeat4Body,
	"feat5Title": i18n.SiteFeat5Title, "feat5Body": i18n.SiteFeat5Body,
	"feat6Title": i18n.SiteFeat6Title, "feat6Body": i18n.SiteFeat6Body,
	"feat7Title": i18n.SiteFeat7Title, "feat7Body": i18n.SiteFeat7Body,
	"feat8Title": i18n.SiteFeat8Title, "feat8Body": i18n.SiteFeat8Body,
	"orbitTitle":  i18n.SiteOrbitTitle,
	"orbit1Title": i18n.SiteOrbit1Title, "orbit1Em": i18n.SiteOrbit1Em, "orbit1Body": i18n.SiteOrbit1Body,
	"orbit2Title": i18n.SiteOrbit2Title, "orbit2Em": i18n.SiteOrbit2Em, "orbit2Body": i18n.SiteOrbit2Body,
	"orbit3Title": i18n.SiteOrbit3Title, "orbit3Em": i18n.SiteOrbit3Em, "orbit3Body": i18n.SiteOrbit3Body,
	"orbit4Title": i18n.SiteOrbit4Title, "orbit4Em": i18n.SiteOrbit4Em, "orbit4Body": i18n.SiteOrbit4Body,
	"finalTitle": i18n.SiteFinalTitle, "finalEm": i18n.SiteFinalEm,
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
	"download":      `<path d="M12 15V3"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/>`,
	"server":        `<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01"/><path d="M6 18h.01"/>`,
}

// platforms 是下载页的平台，按展示顺序排列；os 为空的平台尚未提供安装包。
var platforms = []struct {
	id, name, icon, os string
}{
	{"windows", "Windows", "desktop", release.OSWindows},
	{"macos", "macOS", "desktop", release.OSDarwin},
	{"linux", "Linux", "desktop", release.OSLinux},
	{"android", "Android", "mobile", ""},
	{"ios", "iOS", "mobile", ""},
}

// executorPlatforms 是下载页执行器一节的系统，按展示顺序排列。
var executorPlatforms = []struct{ name, os string }{
	{"Linux", release.OSLinux}, {"Windows", release.OSWindows}, {"macOS", release.OSDarwin},
}

// formatLabels 是不随语言变化的安装包名称，按格式索引。
var formatLabels = map[string]string{
	release.FormatAppImage: "AppImage",
	release.FormatDeb:      "Debian / Ubuntu",
	release.FormatRPM:      "Fedora / openSUSE",
	release.FormatPacman:   "Arch Linux",
}

// archLabels 是安装包与执行器的处理器架构名称。
var archLabels = map[string]string{"amd64": "x64", "arm64": "ARM64"}

// 产品站的页面。
const (
	pageHome     = "home"
	pageDownload = "download"
	pageNotFound = "notFound"
	pagePricing  = "pricing"
)

// Home 是产品首页随部署状态变化的内容。
type Home struct {
	// Pricing 是首页价格区块的内容，为空时不展示价格区块且始终展示自部署介绍。
	Pricing *Pricing
	// RegistrationOpen 表示任何人都可以注册账号，价格区块的入口指向注册页。
	RegistrationOpen bool
}

// Pricing 是首页价格区块展示的套餐与附加商品，都为空时不显示价格区块。
type Pricing struct {
	Plans  []PricingPlan
	Extras PricingExtras
}

// Available 表示是否有可展示的套餐或积分商品；空价格不应作为页头入口。
func (p *Pricing) Available() bool {
	return p != nil && (len(p.Plans) > 0 || len(p.Extras.Items) > 0)
}

// PricingPlan 是价格区块中的一个套餐，SeatLimit 为 0 表示不限席位；Names 的键为语言标签。
type PricingPlan struct {
	Names     map[string]string
	SeatLimit int
	Prices    []PricingPrice
}

// PricingPrice 是价格展示数据，Template 接收 Price 模板变量，Amount 以币种最小单位计。
type PricingPrice struct {
	Template i18n.Key
	Currency string
	Amount   int64
}

// PricingExtras 是价格区块中的附加商品分组。
type PricingExtras struct {
	Title i18n.Key
	Body  i18n.Key
	Items []PricingItem
}

// PricingItem 是带本地化数量说明的商品价格。
type PricingItem struct {
	Label    i18n.Key
	Count    int64
	Currency string
	Amount   int64
}

// Option 调整产品站服务的可选内容。
type Option func(*Service)

// WithCapabilities 设置产品站的能力开关，决定额外页面与页头页脚入口。
func WithCapabilities(caps Capabilities) Option {
	return func(s *Service) { s.caps = caps }
}

// WithText 把额外文案键合并进页面模板的 Text。
func WithText(text map[string]i18n.Key) Option {
	return func(s *Service) { s.text = text }
}

// Service 在根路径下按语言目录输出产品首页与客户端下载页，其余路径输出 404 页面。
type Service struct {
	stylesheet string
	clients    *releaseclients.Catalog
	home       func(context.Context) (Home, error)
	template   *template.Template
	caps       Capabilities
	text       map[string]i18n.Key
}

// NewService 创建产品站服务；stylesheet 保留与文档站点共用的样式地址参数，clients 为服务器提供下载的安装包与执行器，home 不能为 nil，在每次输出首页时读取随部署状态变化的内容。
func NewService(stylesheet string, clients *releaseclients.Catalog, home func(context.Context) (Home, error), options ...Option) *Service {
	service := &Service{stylesheet: stylesheet, clients: clients, home: home, template: template.Must(template.New("page").Parse(pageTemplateSource))}
	for _, option := range options {
		option(service)
	}
	return service
}

// ServeHTTP 处理产品站请求：根路径按语言偏好跳转到对应语言的首页，语言目录下输出首页与下载页。
func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	requestPath := request.URL.Path
	if requestPath == "" || requestPath == "/" {
		http.Redirect(writer, request, docs.SitePath(docs.PreferredLocale(request), ""), http.StatusFound)
		return
	}
	locale, page, _ := strings.Cut(strings.Trim(requestPath, "/"), "/")
	ctx := request.Context()
	extraPages := s.caps.Pages()
	if docs.LanguageTag(locale) == "" || (page != "" && page != docs.SiteDownloadPage && page != pagePricing && !slices.Contains(extraPages, page)) {
		s.write(writer, request, http.StatusNotFound, s.buildView(ctx, docs.PreferredLocale(request), pageNotFound))
		return
	}
	if target := docs.SitePath(locale, page); requestPath != target {
		http.Redirect(writer, request, target, http.StatusMovedPermanently)
		return
	}
	current := pageHome
	if page != "" {
		current = page
	}
	view := s.buildView(ctx, locale, current)
	if current == pagePricing && !view.Pricing {
		s.write(writer, request, http.StatusNotFound, s.buildView(ctx, docs.PreferredLocale(request), pageNotFound))
		return
	}
	s.write(writer, request, http.StatusOK, view)
}

// buildView 构造指定页面的模板数据，并带上读取到的价格区块、自部署介绍与注册策略。
func (s *Service) buildView(ctx context.Context, locale, page string) pageView {
	view := s.newView(locale, page)
	home, err := s.home(ctx)
	if err != nil {
		// 读取套餐失败时页面照常输出，只是不显示价格区块。
		slog.WarnContext(ctx, "读取产品首页套餐失败", "error", err)
	}
	view.applyHome(home)
	return view
}

// pageView 是页面模板的数据。
type pageView struct {
	Lang           string
	Product        string
	Copyright      string
	Stylesheet     string
	HomePath       string
	DocsPath       string
	DownloadPath   string
	DeploymentPath string
	ComputersPath  string
	AppPath        string
	Text           map[string]string
	Platforms      []platformView
	Executors      []executorView
	ClientVersion  string
	Languages      []languageView
	Icons          map[string]template.HTML
	ThemeStyle     template.CSS
	ThemeScript    template.JS
	Page           string
	Caps           Capabilities
	// SelfHost 表示首页展示自部署介绍与部署入口。
	SelfHost bool
	Plans    []planView
	// Items 是价格区块中的附加商品。
	Items      []priceItemView
	ItemsTitle string
	ItemsBody  string
	// Pricing 表示展示价格区块。
	Pricing bool
	// SignUpPath 是价格区块入口的地址，注册开放时为注册页，否则为应用。
	SignUpPath string
}

// priceItemView 是价格区块附加商品的模板数据。
type priceItemView struct {
	Label string
	Price string
}

// planView 是价格区块一个套餐的模板数据。
type planView struct {
	Name   string
	Seats  string
	Prices []string
}

// platformView 是下载页一个平台的模板数据；没有安装包时 Status 说明原因。
type platformView struct {
	ID        string
	Name      string
	Icon      string
	Downloads []downloadView
	Status    string
}

// executorView 是下载页执行器一节中一个系统的模板数据；没有执行器时 Status 说明原因。
type executorView struct {
	Name      string
	Downloads []downloadView
	Status    string
}

// downloadView 是一个安装包或执行器的模板数据。
type downloadView struct {
	Label string
	Size  string
	URL   string
}

// languageView 是语言切换项的模板数据。
type languageView struct {
	Label   string
	Lang    string
	Path    string
	Current bool
}

// newView 生成指定语言、指定页面的页面数据。
func (s *Service) newView(locale, page string) pageView {
	tag := docs.LanguageTag(locale)
	view := pageView{
		Lang: tag, Product: brand.Current().Name(tag), Stylesheet: s.stylesheet,
		HomePath: docs.SitePath(locale, ""), DocsPath: docs.PagePath(locale, ""),
		DownloadPath:   docs.SitePath(locale, docs.SiteDownloadPage),
		DeploymentPath: docs.PagePath(locale, "deployment"), ComputersPath: docs.PagePath(locale, "integrations/computers"), AppPath: domain.WebAppPath,
		Text: i18n.LocalizeMap(tag, textKeys), Icons: icons, Page: page, Caps: s.caps,
		ThemeStyle: ThemeStyle, ThemeScript: ThemeScript,
	}
	maps.Copy(view.Text, i18n.LocalizeMap(tag, s.text))
	view.Copyright = docs.Copyright(locale)
	if page == pageDownload {
		view.Platforms, view.Executors = s.platformViews(tag), s.executorViews(tag)
		if version := s.clients.Version(); version != "" {
			view.ClientVersion = i18n.LocalizeTemplate(tag, i18n.SiteDownloadVersion, map[string]any{"Version": version})
		}
	}
	for _, candidate := range docs.Locales {
		// 下载页与能力页面切换语言时停留在该页，其余页面切换到对应语言的首页。
		target := docs.SitePath(candidate, "")
		if page != pageHome && page != pageNotFound {
			target = docs.SitePath(candidate, page)
		}
		view.Languages = append(view.Languages, languageView{
			Label: docs.LanguageLabel(candidate), Lang: docs.LanguageTag(candidate),
			Path: target, Current: candidate == locale,
		})
	}
	return view
}

// applyHome 按价格区块与注册策略填入首页：提供价格区块时展示套餐与附加商品并隐藏自部署介绍；不提供时不展示价格，始终展示自部署介绍。
func (v *pageView) applyHome(home Home) {
	v.SelfHost = home.Pricing == nil
	if home.Pricing == nil {
		return
	}
	v.SignUpPath = v.AppPath
	if home.RegistrationOpen {
		v.SignUpPath = v.AppPath + "#/register"
	}
	printer := message.NewPrinter(language.MustParse(v.Lang))
	unlimited, _ := i18n.Localize(v.Lang, i18n.SitePricingUnlimited)
	for _, plan := range home.Pricing.Plans {
		view := planView{Name: plan.Names[v.Lang], Seats: unlimited}
		if plan.SeatLimit > 0 {
			view.Seats = i18n.LocalizeTemplate(v.Lang, i18n.SitePricingSeats, map[string]any{"Count": plan.SeatLimit})
		}
		for _, price := range plan.Prices {
			view.Prices = append(view.Prices, i18n.LocalizeTemplate(v.Lang, price.Template, map[string]any{"Price": formatMoney(printer, price.Amount, price.Currency)}))
		}
		v.Plans = append(v.Plans, view)
	}
	v.ItemsTitle, _ = i18n.Localize(v.Lang, home.Pricing.Extras.Title)
	v.ItemsBody, _ = i18n.Localize(v.Lang, home.Pricing.Extras.Body)
	for _, item := range home.Pricing.Extras.Items {
		v.Items = append(v.Items, priceItemView{
			Label: i18n.LocalizeTemplate(v.Lang, item.Label, map[string]any{"Count": printer.Sprint(number.Decimal(item.Count))}),
			Price: formatMoney(printer, item.Amount, item.Currency),
		})
	}
	v.Pricing = len(v.Plans) > 0 || len(v.Items) > 0
}

// formatMoney 按语言显示以币种最小单位计的金额与币种符号，如 ￥99.00；无法识别的币种按 2 位小数并附币种代码。
func formatMoney(printer *message.Printer, amount int64, code string) string {
	unit, err := currency.ParseISO(code)
	if err != nil {
		return fmt.Sprintf("%.2f %s", float64(amount)/100, code)
	}
	scale, _ := currency.Standard.Rounding(unit)
	return printer.Sprint(currency.Symbol(unit)) + printer.Sprint(number.Decimal(float64(amount)/math.Pow10(scale), number.Scale(scale)))
}

// platformViews 按平台归组服务器提供的安装包，移动端显示即将推出，未提供安装包的桌面平台显示未提供。
func (s *Service) platformViews(tag string) []platformView {
	unavailable, _ := i18n.Localize(tag, i18n.SiteDownloadUnavailable)
	comingSoon, _ := i18n.Localize(tag, i18n.SiteDownloadComingSoon)
	universal, _ := i18n.Localize(tag, i18n.SiteDownloadMacUniversal)
	views := make([]platformView, 0, len(platforms))
	for _, platform := range platforms {
		view := platformView{ID: platform.id, Name: platform.name, Icon: platform.icon}
		for _, file := range s.clients.Installers() {
			if file.OS != platform.os {
				continue
			}
			label := formatLabels[file.Format] + " · " + archLabels[file.Arch]
			switch file.Format {
			case release.FormatEXE:
				label = archLabels[file.Arch]
			case release.FormatDMG:
				label = universal
			}
			view.Downloads = append(view.Downloads, downloadView{
				Label: label, Size: fmt.Sprintf("%.1f MB", float64(file.Size)/(1<<20)), URL: releaseclients.URL(file.Name, file.SHA256),
			})
		}
		switch {
		case platform.os == "":
			view.Status = comingSoon
		case len(view.Downloads) == 0:
			view.Status = unavailable
		}
		views = append(views, view)
	}
	return views
}

// executorViews 按系统归组服务器提供的执行器，未提供执行器的系统显示未提供。
func (s *Service) executorViews(tag string) []executorView {
	unavailable, _ := i18n.Localize(tag, i18n.SiteDownloadUnavailable)
	views := make([]executorView, 0, len(executorPlatforms))
	for _, platform := range executorPlatforms {
		view := executorView{Name: platform.name}
		for _, executor := range s.clients.Executors() {
			if executor.OS == platform.os {
				view.Downloads = append(view.Downloads, downloadView{
					Label: archLabels[executor.Arch], Size: fmt.Sprintf("%.1f MB", float64(executor.Size)/(1<<20)), URL: releaseclients.URL(executor.Name, executor.SHA256),
				})
			}
		}
		if len(view.Downloads) == 0 {
			view.Status = unavailable
		}
		views = append(views, view)
	}
	return views
}

// write 渲染页面模板并写入响应。
func (s *Service) write(writer http.ResponseWriter, request *http.Request, status int, view pageView) {
	var body bytes.Buffer
	if err := s.template.Execute(&body, view); err != nil {
		slog.ErrorContext(request.Context(), "渲染产品首页失败", "path", request.URL.Path, "error", err)
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
