//go:build server

// Package publicweb 提供网站渠道的公开嵌入脚本和访客聊天页。
package publicweb

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/embedhost"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/webasset"
	"github.com/runforyou-ai/support/str"
)

// themePlaceholder 是嵌入脚本中挂件主题变量的注入位置。
const themePlaceholder = "/*CV_THEME*/"

// copyPlaceholder 是嵌入脚本中挂件文案的注入位置。
const copyPlaceholder = "/*CV_COPY*/ null"

// sdkPlaceholder 是嵌入脚本中宿主页对象名的注入位置。
const sdkPlaceholder = "/*CV_SDK*/ null"

// Lookup 按渠道标识读取公开网站渠道。
type Lookup func(context.Context, string) (*channelaction.PublicWebsiteChannel, error)

// pageView 定义访客聊天页模板内容。
type pageView struct {
	Lang          string
	ChannelID     string
	Title         string
	TitleInitials string
	Greeting      string
	// Welcome 与 Headline 是首页问候语，渠道未设置时为默认文案。
	Welcome  string
	Headline string
	// HomeBlockOrder 按卡片类型给出首页展示顺序，HomeBlockOff 标记关闭的卡片。
	HomeBlockOrder     map[string]int
	HomeBlockOff       map[string]bool
	HomeLinks          []domain.WebsiteHomeLink
	HomeEnabled        bool
	HelpEnabled        bool
	AttachmentsEnabled bool
	EmojiEnabled       bool
	RatingEnabled      bool
	// HelpSearchMaxLength 是帮助中心搜索内容的最大字符数，与知识库检索上限一致。
	HelpSearchMaxLength int
	// MultipleConversations 为假时访客界面只提供一个对话。
	MultipleConversations bool
	EmptyMessage          string
	Shell                 string
	NotFound              bool
	ShowWidgetControls    bool
	Preview               bool
	Copy                  map[string]string
	ThemeCSS              template.CSS
	MessengerCSS          template.CSS
	ComposerEmojis        template.JS
	ChatJS                template.JS
	MarkdownVersion       string
	FrameAncestors        string
}

// previewHostView 定义管理端挂件预览宿主页内容。
type previewHostView struct {
	Lang       string
	Title      string
	StageLabel string
}

// pageTemplate 是访客聊天页模板。
var pageTemplate = template.Must(template.New("chat").Parse(pageHTML))

// previewHostTemplate 是管理端挂件预览宿主页模板。
var previewHostTemplate = template.Must(template.New("preview").Parse(previewHTML))

// EmbedService 提供 /embed/widget.js 和嵌入聊天框页面。
type EmbedService struct {
	lookup Lookup
}

// NewEmbedService 创建网站嵌入公开服务。
func NewEmbedService(lookup Lookup) *EmbedService {
	return &EmbedService{lookup: lookup}
}

// ServeHTTP 处理嵌入脚本和嵌入聊天框请求。
func (s *EmbedService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !allowPublicMethod(writer, request) {
		return
	}
	switch {
	case request.URL.Path == "/widget.js":
		s.writeWidgetScript(writer, request)
	case request.URL.Path == "/preview/frame":
		// 返回管理界面使用的 Messenger 预览页。
		locale := i18n.PreferredCustomerLocale(request.Header.Get("Accept-Language"))
		page := baseView("preview", defaultTheme(), locale)
		page.Preview = true
		page.ShowWidgetControls = true
		// 预览框同时允许管理端顶层和同源预览宿主页。
		page.FrameAncestors = "* wails:"
		page.Title = i18n.LocalizeCustomerTemplate(locale, i18n.MessengerDefaultTitle, nil)
		page.TitleInitials = nameInitials(page.Title)
		page.Greeting = page.Copy["conversationPrompt"]
		if err := writePage(writer, page, http.StatusOK); err != nil {
			slog.WarnContext(request.Context(), "写入网站渠道 Messenger 预览框失败", "error", err)
		}
	case strings.HasPrefix(request.URL.Path, "/widget/"):
		writeChatPage(writer, request, s.lookup, strings.TrimPrefix(request.URL.Path, "/widget/"), "embed")
	default:
		http.NotFound(writer, request)
	}
}

// ChatService 提供独立聊天链接页面。
type ChatService struct {
	lookup Lookup
}

// NewChatService 创建独立聊天页公开服务。
func NewChatService(lookup Lookup) *ChatService {
	return &ChatService{lookup: lookup}
}

// ServeHTTP 处理独立聊天链接请求。
func (s *ChatService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !allowPublicMethod(writer, request) {
		return
	}
	channelID := strings.TrimPrefix(request.URL.Path, "/")
	if name, ok := strings.CutPrefix(channelID, "assets/"); ok {
		if asset, found := markdownAssetsByName[name]; found {
			// 地址携带当前内容版本时长期缓存，其余地址每次按 ETag 校验。
			cacheControl := webasset.RevalidateCache
			if request.URL.Query().Get("v") == markdownAssetVersion {
				cacheControl = webasset.ImmutableCache
			}
			asset.Serve(writer, request, cacheControl)
			return
		}
	}
	if channelID == "preview" {
		if err := writePreviewHost(writer, request); err != nil {
			slog.WarnContext(request.Context(), "写入网站渠道挂件预览失败", "error", err)
		}
		return
	}
	writeChatPage(writer, request, s.lookup, channelID, "link")
}

// writePreviewHost 写入管理端挂件预览宿主页。
func writePreviewHost(writer http.ResponseWriter, request *http.Request) error {
	acceptLanguage := request.Header.Get("Accept-Language")
	title, lang := i18n.Localize(acceptLanguage, i18n.MessengerPreviewTitle)
	stageLabel, _ := i18n.Localize(acceptLanguage, i18n.MessengerPreviewStageLabel)
	view := previewHostView{Lang: lang, Title: title, StageLabel: stageLabel}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Vary", "Accept-Language")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "frame-ancestors * wails:")
	return previewHostTemplate.Execute(writer, view)
}

// allowPublicMethod 仅允许 GET 和 HEAD。
func allowPublicMethod(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}
	writer.Header().Set("Allow", "GET, HEAD")
	http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

// writeWidgetScript 返回按渠道内联主题色的网站嵌入脚本。
func (s *EmbedService) writeWidgetScript(writer http.ResponseWriter, request *http.Request) {
	theme := defaultTheme()
	channelID := strings.TrimSpace(request.URL.Query().Get("id"))
	cacheControl := "public, max-age=300"
	if request.URL.Query().Get("preview") == "1" {
		cacheControl = "no-store"
	}
	if channelID != "" {
		channel, err := s.lookup(request.Context(), channelID)
		if errors.Is(err, channelaction.ErrNotFound) {
			slog.InfoContext(request.Context(), "网站嵌入脚本渠道不存在", "channel_id", channelID)
			writeWidgetJavaScript(request.Context(), writer, http.StatusNotFound, "no-store", channelID, []byte("/* Website channel not found. */"))
			return
		}
		if err != nil {
			slog.WarnContext(request.Context(), "读取网站嵌入脚本渠道失败", "channel_id", channelID, "error", err)
			writeWidgetJavaScript(request.Context(), writer, http.StatusInternalServerError, "no-store", channelID, []byte("/* Website channel unavailable. */"))
			return
		}
		host := embedRequestHost(request)
		if !embedhost.Allows(channel.AllowedEmbedHosts, host) {
			slog.InfoContext(request.Context(), "网站渠道拒绝未允许的嵌入脚本来源", "channel_id", channelID, "host", host)
			writeWidgetJavaScript(request.Context(), writer, http.StatusForbidden, "no-store", channelID, []byte("/* This website is not allowed to use the channel. */"))
			return
		}
		theme = parseTheme(channel.ThemeColor)
	}
	// 生成挂件主题变量。
	hostCSS := fmt.Sprintf(
		":host{--cv-theme:%s;--cv-on-theme:%s;--cv-focus:%s;--cv-launcher-shadow:%s}",
		theme.Color,
		theme.OnColor,
		theme.Focus,
		theme.LauncherShadow,
	)
	// 按访客语言偏好生成挂件文案。
	copyJSON, _ := json.Marshal(i18n.LocalizeCustomerMap(i18n.PreferredCustomerLocale(request.Header.Get("Accept-Language")), map[string]i18n.Key{
		"dialog": i18n.MessengerWidgetDialog,
		"open":   i18n.MessengerWidgetOpen,
		"close":  i18n.MessengerClose,
	}))
	// 生成包含主题变量与挂件文案的挂件脚本。
	script := bytes.Replace(widgetScript, []byte(themePlaceholder), []byte(hostCSS), 1)
	script = bytes.Replace(script, []byte(copyPlaceholder), copyJSON, 1)
	// 按当前品牌生成宿主页对象名：全局对象为 SDKName，设置对象为首字母小写加 Settings，打开属性为 data-小写名称-open。
	sdkName := brand.Current().SDKName
	sdkJSON, _ := json.Marshal(map[string]string{
		"global":        sdkName,
		"settings":      strings.ToLower(sdkName[:1]) + sdkName[1:] + "Settings",
		"openAttribute": "data-" + strings.ToLower(sdkName) + "-open",
	})
	script = bytes.Replace(script, []byte(sdkPlaceholder), sdkJSON, 1)
	writeWidgetJavaScript(request.Context(), writer, http.StatusOK, cacheControl, channelID, script)
}

// writeWidgetJavaScript 写入网站嵌入脚本响应。
func writeWidgetJavaScript(ctx context.Context, writer http.ResponseWriter, status int, cacheControl string, channelID string, script []byte) {
	writer.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	writer.Header().Set("Cache-Control", cacheControl)
	writer.Header().Set("Vary", "Origin, Referer, Accept-Language")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	if _, err := writer.Write(script); err != nil {
		slog.WarnContext(ctx, "写入网站嵌入脚本响应失败", "channel_id", channelID, "status", status, "error", err)
	}
}

// writeChatPage 渲染公开聊天页。
func writeChatPage(writer http.ResponseWriter, request *http.Request, lookup Lookup, channelID string, entry string) {
	channel, err := lookup(request.Context(), channelID)
	if errors.Is(err, channelaction.ErrNotFound) {
		// 返回聊天入口不存在时的页面。
		locale := i18n.PreferredCustomerLocale(request.Header.Get("Accept-Language"))
		page := baseView(entry, defaultTheme(), locale)
		page.NotFound = true
		messages := i18n.LocalizeCustomerMap(locale, map[string]i18n.Key{
			"title":   i18n.MessengerUnavailableTitle,
			"message": i18n.MessengerUnavailableMessage,
		})
		page.Title = messages["title"]
		page.EmptyMessage = messages["message"]
		if err := writePage(writer, page, http.StatusNotFound); err != nil {
			slog.WarnContext(request.Context(), "写入网站渠道不可用页面失败", "channel_id", channelID, "entry", entry, "error", err)
			return
		}
		slog.InfoContext(request.Context(), "网站渠道聊天入口不存在", "channel_id", channelID, "entry", entry)
		return
	}
	if err != nil {
		slog.WarnContext(request.Context(), "读取公开网站渠道失败", "channel_id", channelID, "entry", entry, "error", err)
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if entry == "embed" {
		host := embedRequestHost(request)
		if !embedhost.Allows(channel.AllowedEmbedHosts, host) {
			// 拒绝未允许的网站加载聊天框。
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-store")
			writer.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
			writer.Header().Set("Vary", "Origin, Referer")
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			writer.WriteHeader(http.StatusForbidden)
			slog.InfoContext(request.Context(), "网站渠道拒绝未允许的嵌入聊天框来源", "channel_id", channel.ID, "host", host)
			return
		}
	}
	locale := i18n.PreferredCustomerLocale(request.Header.Get("Accept-Language"))
	if err := writePage(writer, chatView(channel, entry, locale), http.StatusOK); err != nil {
		slog.WarnContext(request.Context(), "写入网站渠道聊天页失败", "channel_id", channel.ID, "entry", entry, "error", err)
		return
	}
	slog.InfoContext(request.Context(), "打开网站渠道聊天页", "channel_id", channel.ID, "entry", entry)
}

// writePage 写入公开聊天 HTML。
func writePage(writer http.ResponseWriter, page pageView, status int) error {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Vary", "Accept-Language")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "frame-ancestors "+page.FrameAncestors)
	writer.WriteHeader(status)
	return pageTemplate.Execute(writer, page)
}

// chatView 按渠道设置生成聊天页。
func chatView(channel *channelaction.PublicWebsiteChannel, entry string, locale domain.CustomerLocale) pageView {
	page := baseView(entry, parseTheme(channel.ThemeColor), locale)
	page.ChannelID = channel.ID
	page.Title = channel.Title
	page.TitleInitials = nameInitials(channel.Title)
	page.HomeLinks = channel.HomeLinks
	page.HomeEnabled = channel.HomeEnabled
	page.HelpEnabled = channel.HelpEnabled
	page.AttachmentsEnabled = channel.AttachmentsEnabled
	page.EmojiEnabled = channel.EmojiEnabled
	page.RatingEnabled = channel.RatingEnabled
	page.MultipleConversations = channel.MultipleConversationsEnabled
	if channel.HomeWelcome != "" {
		page.Welcome = channel.HomeWelcome
	}
	if channel.HomeHeadline != "" {
		page.Headline = channel.HomeHeadline
	}
	for index, block := range channel.HomeBlocks {
		page.HomeBlockOrder[string(block.Type)] = index
		page.HomeBlockOff[string(block.Type)] = !block.Enabled
	}
	page.Greeting = strings.TrimSpace(channel.Greeting)
	if page.Greeting == "" {
		page.Greeting = page.Copy["conversationPrompt"]
	}
	if entry == "embed" {
		page.FrameAncestors = embedhost.FrameAncestors(channel.AllowedEmbedHosts)
	}
	return page
}

// baseView 填充聊天页共用内容。
func baseView(entry string, theme theme, locale domain.CustomerLocale) pageView {
	// 按映射表本地化 Messenger 固定文案。
	messengerText := i18n.LocalizeCustomerMap(locale, messengerCopyMessageKeys)
	page := pageView{
		Shell:                 entry,
		ShowWidgetControls:    entry == "embed",
		Copy:                  messengerText,
		ThemeCSS:              template.CSS(theme.rootCSS()),
		MessengerCSS:          template.CSS(messengerCSS),
		ComposerEmojis:        template.JS(composerEmojisJSON),
		ChatJS:                template.JS(chatJS),
		MarkdownVersion:       markdownAssetVersion,
		FrameAncestors:        "*",
		Lang:                  string(locale),
		Welcome:               messengerText["welcome"],
		Headline:              messengerText["howCanWeHelp"],
		HomeBlockOrder:        make(map[string]int),
		HomeBlockOff:          make(map[string]bool),
		HomeEnabled:           false,
		HelpEnabled:           false,
		AttachmentsEnabled:    true,
		EmojiEnabled:          true,
		RatingEnabled:         true,
		MultipleConversations: false,
		HelpSearchMaxLength:   domain.KnowledgeRetrievalQueryMaxLength,
	}
	// 按默认顺序排列首页卡片。
	for index, blockType := range domain.WebsiteHomeBlockTypes() {
		page.HomeBlockOrder[string(blockType)] = index
	}
	return page
}

// nameInitials 取名称开头的两个字符作为字标，名称为空时返回问号。
func nameInitials(name string) string {
	return cmp.Or(str.Substr(strings.ToUpper(strings.TrimSpace(name)), 0, 2), "?")
}

// messengerCopyMessageKeys 定义访客 Messenger 固定文案，键是模板中 .Copy 的字段名。
var messengerCopyMessageKeys = map[string]i18n.Key{
	"home":                   i18n.MessengerHome,
	"messages":               i18n.MessengerMessages,
	"help":                   i18n.MessengerHelp,
	"message":                i18n.MessengerMessage,
	"close":                  i18n.MessengerClose,
	"attach":                 i18n.MessengerAttach,
	"emoji":                  i18n.MessengerEmoji,
	"demoReply":              i18n.MessengerDemoReply,
	"welcome":                i18n.MessengerWelcome,
	"howCanWeHelp":           i18n.MessengerHowCanWeHelp,
	"startConversation":      i18n.MessengerStartConversation,
	"aiBadge":                i18n.MessengerAIBadge,
	"conversationTab":        i18n.MessengerConversationTab,
	"continueConversation":   i18n.MessengerContinueConversation,
	"recentConversation":     i18n.MessengerRecentConversation,
	"viewAll":                i18n.MessengerViewAll,
	"links":                  i18n.MessengerLinks,
	"replyImmediate":         i18n.MessengerReplyImmediate,
	"replyMinutes":           i18n.MessengerReplyMinutes,
	"replyTenMinutes":        i18n.MessengerReplyTenMinutes,
	"replyHalfHour":          i18n.MessengerReplyHalfHour,
	"replyHour":              i18n.MessengerReplyHour,
	"replyHours":             i18n.MessengerReplyHours,
	"replySoon":              i18n.MessengerReplySoon,
	"replyScheduled":         i18n.MessengerReplyScheduled,
	"noMessages":             i18n.MessengerNoMessages,
	"noMessagesDescription":  i18n.MessengerNoMessagesDescription,
	"searchHelp":             i18n.MessengerSearchHelp,
	"noHelpResults":          i18n.MessengerNoHelpResults,
	"back":                   i18n.MessengerBack,
	"stillNeedHelp":          i18n.MessengerStillNeedHelp,
	"articleCount":           i18n.MessengerArticleCount,
	"articleCountOne":        i18n.MessengerArticleCountOne,
	"collectionCount":        i18n.MessengerCollectionCount,
	"collectionCountOne":     i18n.MessengerCollectionCountOne,
	"helpSearching":          i18n.MessengerHelpSearching,
	"helpSearchFailed":       i18n.MessengerHelpSearchFailed,
	"helpArticleUnavailable": i18n.MessengerHelpArticleUnavailable,
	"conversationPrompt":     i18n.MessengerConversationPrompt,
	"more":                   i18n.MessengerMore,
	"expandWindow":           i18n.MessengerExpandWindow,
	"collapseWindow":         i18n.MessengerCollapseWindow,
	"recordVoice":            i18n.MessengerRecordVoice,
	"playVoice":              i18n.MessengerPlayVoice,
	"pauseVoice":             i18n.MessengerPauseVoice,
	"send":                   i18n.MessengerSend,
	"cancelRecording":        i18n.MessengerCancelRecording,
	"stopRecording":          i18n.MessengerStopRecording,
	"messengerNavigation":    i18n.MessengerNavigation,
	"loading":                i18n.MessengerLoading,
	"retry":                  i18n.MessengerRetry,
	"referenceDeleted":       i18n.MessengerReferenceDeleted,
	"referenceVisitor":       i18n.MessengerReferenceVisitor,
	"referenceAgent":         i18n.MessengerReferenceAgent,
	"referenceReply":         i18n.MessengerReferenceReply,
	"referenceReplying":      i18n.MessengerReferenceReplying,
	"referenceCancel":        i18n.MessengerReferenceCancel,
	"referenceUnavailable":   i18n.MessengerReferenceUnavailable,
	"referenceLatest":        i18n.MessengerReferenceLatest,
	"requestFailed":          i18n.MessengerRequestFailed,
	"identityExpired":        i18n.MessengerIdentityExpired,
	"attachmentUploading":    i18n.MessengerAttachmentUploading,
	"attachmentFailed":       i18n.MessengerAttachmentFailed,
	"attachmentCancel":       i18n.MessengerAttachmentCancel,
	"attachmentReceiving":    i18n.MessengerAttachmentReceiving,
	"attachmentUnavailable":  i18n.MessengerAttachmentUnavailable,
	"sessionOpen":            i18n.MessengerSessionOpen,
	"sessionClosed":          i18n.MessengerSessionClosed,
	"dayToday":               i18n.MessengerDayToday,
	"dayYesterday":           i18n.MessengerDayYesterday,
	"sessionEnded":           i18n.MessengerSessionEnded,
	"memberJoined":           i18n.MessengerMemberJoined,
	"emailCollected":         i18n.MessengerEmailCollected,
	"ratingQuestion":         i18n.MessengerRatingQuestion,
	"ratingResolved":         i18n.MessengerRatingResolved,
	"ratingUnresolved":       i18n.MessengerRatingUnresolved,
	"ratingComment":          i18n.MessengerRatingComment,
	"ratingSubmit":           i18n.MessengerRatingSubmit,
	"ratingThanks":           i18n.MessengerRatingThanks,
	"toolTitle":              i18n.MessengerToolConfirmationTitle,
	"toolConfirm":            i18n.MessengerToolConfirm,
	"toolReject":             i18n.MessengerToolReject,
	"toolDeadline":           i18n.MessengerToolDeadline,
	"toolConfirmed":          i18n.MessengerToolStatusConfirmed,
	"toolSucceeded":          i18n.MessengerToolStatusSucceeded,
	"toolFailed":             i18n.MessengerToolStatusFailed,
	"toolReview":             i18n.MessengerToolStatusReview,
	"toolRejected":           i18n.MessengerToolStatusRejected,
	"toolExpired":            i18n.MessengerToolStatusExpired,
	"toolCancelled":          i18n.MessengerToolStatusCancelled,
}

// embedRequestHost 从公开嵌入请求中读取宿主网站主机。
func embedRequestHost(request *http.Request) string {
	for _, value := range []string{request.Header.Get("Origin"), request.Referer()} {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return ""
}
