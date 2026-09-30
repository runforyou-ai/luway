package domain

// ChannelType 定义渠道类型。
type ChannelType string

const (
	ChannelTypeWebsite               ChannelType = "website"
	ChannelTypeTelegram              ChannelType = "telegram"
	ChannelTypeWeChatOfficialAccount ChannelType = "wechat_official_account"
)

// MessageChannelTypes 返回当前支持管理的消息渠道类型。
func MessageChannelTypes() []ChannelType {
	return []ChannelType{ChannelTypeWebsite, ChannelTypeTelegram, ChannelTypeWeChatOfficialAccount}
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

// ChannelSupportsAgentAssignee 判断渠道是否允许 AI 员工作为会话负责人并对外回复。
func ChannelSupportsAgentAssignee(channelType ChannelType) bool {
	return channelType == ChannelTypeWebsite || channelType == ChannelTypeTelegram
}

// 渠道附件的字节上限与说明字符上限。
const (
	websiteAttachmentLimit int64 = 20 * 1024 * 1024
	telegramInboundLimit   int64 = 20 * 1024 * 1024
	telegramOutboundLimit  int64 = 50 * 1024 * 1024
	websiteCaptionLimit          = 4000
	telegramCaptionLimit         = 1024
	websiteTextLimit             = 8000
	telegramTextLimit            = 4096
)

// ChannelSupportsOutboundAttachment 判断渠道是否支持向客户发送附件。
func ChannelSupportsOutboundAttachment(channelType ChannelType) bool {
	return channelType == ChannelTypeWebsite || channelType == ChannelTypeTelegram
}

// ChannelAttachmentLimit 返回渠道单个外发附件的平台字节上限，渠道没有附件能力时为 0。
func ChannelAttachmentLimit(channelType ChannelType) int64 {
	switch channelType {
	case ChannelTypeWebsite:
		return websiteAttachmentLimit
	case ChannelTypeTelegram:
		return telegramOutboundLimit
	default:
		return 0
	}
}

// ChannelInboundAttachmentLimit 返回渠道单个入站附件的字节上限。
func ChannelInboundAttachmentLimit(channelType ChannelType) int64 {
	switch channelType {
	case ChannelTypeWebsite:
		return websiteAttachmentLimit
	case ChannelTypeTelegram:
		return telegramInboundLimit
	default:
		return 0
	}
}

// ChannelCaptionLimit 返回渠道附件说明的字符上限。
func ChannelCaptionLimit(channelType ChannelType) int {
	if channelType == ChannelTypeTelegram {
		return telegramCaptionLimit
	}
	return websiteCaptionLimit
}

// ChannelTextLimit 返回渠道单条外发文本消息的字符上限。
func ChannelTextLimit(channelType ChannelType) int {
	if channelType == ChannelTypeTelegram {
		return telegramTextLimit
	}
	return websiteTextLimit
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
