package domain

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/support/arr"
)

// ChannelType 定义渠道类型。
type ChannelType string

const (
	ChannelTypeWebsite  ChannelType = "website"
	ChannelTypeTelegram ChannelType = "telegram"
	// ChannelTypeWechatKey 是以公众号 AppID 与 AppSecret 接入的公众号渠道。
	ChannelTypeWechatKey ChannelType = "wechat_official_account_key"
	// ChannelTypeWechatAuthorization 是经部署的微信第三方平台授权接入的公众号渠道。
	ChannelTypeWechatAuthorization ChannelType = "wechat_official_account_authorization"
	ChannelTypeWeComBot            ChannelType = "wecom_bot"
)

// MessageChannelTypes 返回当前支持管理的消息渠道类型。
func MessageChannelTypes() []ChannelType {
	return []ChannelType{ChannelTypeWebsite, ChannelTypeTelegram, ChannelTypeWechatKey, ChannelTypeWechatAuthorization, ChannelTypeWeComBot}
}

// SupportedMessageChannelType 判断消息渠道类型是否受支持。
func SupportedMessageChannelType(channelType ChannelType) bool {
	for _, supportedType := range MessageChannelTypes() {
		if channelType == supportedType {
			return true
		}
	}
	return false
}

// ChannelDelivery 定义渠道把处理人回复送达对方的方式。
type ChannelDelivery string

const (
	// ChannelDeliveryNone 表示渠道不支持向对方发送消息。
	ChannelDeliveryNone ChannelDelivery = "none"
	// ChannelDeliveryWebsite 表示回复由访客页面实时读取。
	ChannelDeliveryWebsite ChannelDelivery = "website"
	// ChannelDeliveryPlatform 表示回复经外部平台投递。
	ChannelDeliveryPlatform ChannelDelivery = "platform"
)

// ChannelAttachmentRule 定义渠道可外发的一类附件。
type ChannelAttachmentRule struct {
	// ContentTypes 是这类附件允许的内容类型，为空时不限类型。
	ContentTypes []string
	ByteLimit    int64
}

// ChannelCapabilities 定义渠道类型的服务对象与收发能力。
type ChannelCapabilities struct {
	Audience ServiceAudience
	// PlatformTypes 是与该类型接入同一外部平台账号体系的全部渠道类型，同一平台账号下的同一外部编号在这些渠道中代表同一个人。
	PlatformTypes []ChannelType
	Delivery      ChannelDelivery
	// Typing 表示处理人输入时向对方展示正在输入。
	Typing bool
	// ToolConfirmation 表示能向对方展示操作确认卡片并接收确认或拒绝。
	ToolConfirmation bool
	// Quote 表示对外消息可以引用会话中的消息。
	Quote bool
	// Connection 表示收发都经服务端维持的平台长连接，外发只能由持有连接的服务端实例执行。
	Connection bool
	// ReplyWindow 表示只能在对方互动开启的回复窗口内发送，窗口规则由渠道适配器给出。
	ReplyWindow bool
	// TextLimit 是单条对外文本消息的字符上限。
	TextLimit int
	// TextByteLimit 是单条对外文本消息的 UTF-8 字节上限，为 0 时不限字节数。
	TextByteLimit int
	// CaptionLimit 是附件说明的字符上限，为 0 时附件不带说明。
	CaptionLimit int
	// Attachments 是可外发的附件类别，为空时不支持外发附件。
	Attachments []ChannelAttachmentRule
	// InboundAttachmentLimit 是单个入站附件的字节上限。
	InboundAttachmentLimit int64
}

// 渠道附件的字节上限与文本字符上限。
const (
	websiteAttachmentLimit int64 = 20 * 1024 * 1024
	telegramInboundLimit   int64 = 20 * 1024 * 1024
	telegramOutboundLimit  int64 = 50 * 1024 * 1024
	wecomBotInboundLimit   int64 = 20 * 1024 * 1024
	// wechatInboundLimit 按公众号临时素材中视频 10MB 的上限设定。
	wechatInboundLimit int64 = 10 * 1024 * 1024
	// wechatImageLimit 与 wechatVoiceLimit 是公众号临时素材中图片与语音的字节上限。
	wechatImageLimit     int64 = 10 * 1024 * 1024
	wechatVoiceLimit     int64 = 2 * 1024 * 1024
	websiteCaptionLimit        = 4000
	telegramCaptionLimit       = 1024
	websiteTextLimit           = 8000
	telegramTextLimit          = 4096
	// wecomBotTextLimit 是平台 Markdown 正文的 UTF-8 字节上限，字符数不超过字节数。
	wecomBotTextLimit = 20480
	// wechatTextLimit 是公众号客服消息文本的 UTF-8 字节上限，字符数不超过字节数。
	wechatTextLimit = 2048
)

// wechatCapabilities 是两种公众号渠道共用的能力：一条消息对应一次客服消息请求，附件只发图片与语音且不带说明，不支持引用。
var wechatCapabilities = ChannelCapabilities{
	Audience:      ServiceAudienceCustomer,
	PlatformTypes: []ChannelType{ChannelTypeWechatKey, ChannelTypeWechatAuthorization},
	Delivery:      ChannelDeliveryPlatform,
	ReplyWindow:   true,
	TextLimit:     wechatTextLimit,
	TextByteLimit: wechatTextLimit,
	Attachments: []ChannelAttachmentRule{
		{ContentTypes: []string{"image/jpeg", "image/png", "image/gif"}, ByteLimit: wechatImageLimit},
		{ContentTypes: []string{"audio/amr", "audio/mpeg", "audio/mp3"}, ByteLimit: wechatVoiceLimit},
	},
	InboundAttachmentLimit: wechatInboundLimit,
}

// channelCapabilities 按渠道类型登记能力。
var channelCapabilities = map[ChannelType]ChannelCapabilities{
	ChannelTypeWebsite: {
		Audience: ServiceAudienceCustomer, PlatformTypes: []ChannelType{ChannelTypeWebsite}, Delivery: ChannelDeliveryWebsite,
		Typing: true, ToolConfirmation: true, Quote: true, TextLimit: websiteTextLimit, CaptionLimit: websiteCaptionLimit,
		Attachments: []ChannelAttachmentRule{{ByteLimit: websiteAttachmentLimit}}, InboundAttachmentLimit: websiteAttachmentLimit,
	},
	ChannelTypeTelegram: {
		Audience: ServiceAudienceCustomer, PlatformTypes: []ChannelType{ChannelTypeTelegram}, Delivery: ChannelDeliveryPlatform,
		Quote: true, TextLimit: telegramTextLimit, CaptionLimit: telegramCaptionLimit,
		Attachments: []ChannelAttachmentRule{{ByteLimit: telegramOutboundLimit}}, InboundAttachmentLimit: telegramInboundLimit,
	},
	ChannelTypeWechatKey:           wechatCapabilities,
	ChannelTypeWechatAuthorization: wechatCapabilities,
	ChannelTypeWeComBot: {
		Audience: ServiceAudienceEmployee, PlatformTypes: []ChannelType{ChannelTypeWeComBot}, Delivery: ChannelDeliveryPlatform, Connection: true,
		TextLimit: wecomBotTextLimit, TextByteLimit: wecomBotTextLimit, InboundAttachmentLimit: wecomBotInboundLimit,
	},
}

// ChannelCapabilitiesOf 返回渠道类型的能力，未登记的类型不支持任何收发能力。
func ChannelCapabilitiesOf(channelType ChannelType) ChannelCapabilities {
	if capabilities, ok := channelCapabilities[channelType]; ok {
		return capabilities
	}
	return ChannelCapabilities{Delivery: ChannelDeliveryNone, PlatformTypes: []ChannelType{channelType}}
}

// ChannelTypesWith 按 MessageChannelTypes 的顺序返回能力满足 match 的渠道类型。
func ChannelTypesWith(match func(ChannelCapabilities) bool) []ChannelType {
	return arr.Filter(MessageChannelTypes(), func(channelType ChannelType) bool { return match(ChannelCapabilitiesOf(channelType)) })
}

// Outbound 判断渠道支持处理人与 AI 员工向对方发送消息。
func (c ChannelCapabilities) Outbound() bool {
	return c.Delivery == ChannelDeliveryWebsite || c.Delivery == ChannelDeliveryPlatform
}

// ViaPlatform 判断渠道的对外消息经外部平台投递。
func (c ChannelCapabilities) ViaPlatform() bool {
	return c.Delivery == ChannelDeliveryPlatform
}

// TextFits 判断文本不超过渠道单条对外文本消息的字符与字节上限。
func (c ChannelCapabilities) TextFits(text string) bool {
	return utf8.RuneCountInString(text) <= c.TextLimit && (c.TextByteLimit == 0 || len(text) <= c.TextByteLimit)
}

// AttachmentByteLimit 返回内容类型为 contentType 的附件可外发的字节上限，渠道不接受该类型时返回 false。
func (c ChannelCapabilities) AttachmentByteLimit(contentType string) (int64, bool) {
	// 内容类型比较时忽略参数与大小写。
	mediaType, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(contentType)), ";")
	mediaType = strings.TrimSpace(mediaType)
	for _, rule := range c.Attachments {
		if len(rule.ContentTypes) == 0 || slices.Contains(rule.ContentTypes, mediaType) {
			return rule.ByteLimit, true
		}
	}
	return 0, false
}

// ChannelRoutingTargetType 定义渠道会话流转目标类型。
type ChannelRoutingTargetType string

const (
	ChannelRoutingTargetTypePublicQueue ChannelRoutingTargetType = "public_queue"
	ChannelRoutingTargetTypeTeam        ChannelRoutingTargetType = "team"
	ChannelRoutingTargetTypeMember      ChannelRoutingTargetType = "member"
)

// WebsiteHomeLink 定义网站 Messenger 首页展示的一条链接。
type WebsiteHomeLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// WebsiteHomeBlockType 定义网站 Messenger 首页卡片类型。
type WebsiteHomeBlockType string

const (
	WebsiteHomeBlockRecentConversation WebsiteHomeBlockType = "recent_conversation"
	WebsiteHomeBlockStartConversation  WebsiteHomeBlockType = "start_conversation"
	WebsiteHomeBlockLinks              WebsiteHomeBlockType = "links"
)

// WebsiteHomeBlockTypes 返回首页卡片类型的默认顺序。
func WebsiteHomeBlockTypes() []WebsiteHomeBlockType {
	return []WebsiteHomeBlockType{WebsiteHomeBlockRecentConversation, WebsiteHomeBlockStartConversation, WebsiteHomeBlockLinks}
}

// WebsiteHomeBlock 定义网站 Messenger 首页的一张卡片及其开关。
type WebsiteHomeBlock struct {
	Type    WebsiteHomeBlockType `json:"type"`
	Enabled bool                 `json:"enabled"`
}
