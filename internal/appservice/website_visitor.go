package appservice

import (
	"context"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// WebsiteVisitorTokenHeader 是访客直传本地对象和调用公开接口使用的令牌请求头。
	WebsiteVisitorTokenHeader = "X-Visitor-Token"
	// CustomerTokenHeader 是网站登录用户调用公开接口、业务系统转发 Telegram 消息时携带客户签名身份的请求头。
	CustomerTokenHeader = "X-Customer-Token"
	// WebsiteCustomerIdentityInvalidReason 是签名身份失效错误的稳定原因码。
	WebsiteCustomerIdentityInvalidReason = "customer_identity_invalid"
)

// WebsiteVisitorMeta 携带网站访客请求的本地化信息、访客令牌、签名身份与请求来源信息。
type WebsiteVisitorMeta struct {
	Locale CustomerLocale
	Token  string
	// CustomerToken 是请求携带的签名身份原文，Customer 是其验签结果；匿名访客两者均为空。
	CustomerToken string
	Customer      *WebsiteVisitorCustomer
	// UserAgent 与 Country 是请求头中的浏览器标识和可信代理提供的国家代码。
	UserAgent string
	Country   string
}

// WebsiteVisitorCustomer 是验签通过的网站登录用户。
type WebsiteVisitorCustomer struct {
	WorkspaceID string
	UserID      string
	Name        string
	Email       string
	Profile     domain.SignedContactProfile
	ExpiresAt   time.Time
}

// WebsiteVisitorPage 定义访客发送消息时所在的宿主页面与浏览器环境。
type WebsiteVisitorPage struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Referrer string `json:"referrer"`
	Language string `json:"language"`
	TimeZone string `json:"timeZone"`
}

// WebsiteVisitorServiceSession 定义客户线程最新客服处理状态与当前接待状态。
type WebsiteVisitorServiceSession struct {
	ID        string                  `json:"id"`
	Status    string                  `json:"status"`
	Reception WebsiteVisitorReception `json:"reception"`
}

// WebsiteVisitorReception 定义访客端展示的接待方、在线状态与回复预期：handlerType 为空表示由团队或公共队列接待，reply 为 immediate、按真人首响估计的 minutes、ten_minutes、half_hour、hour 与 hours、soon、scheduled 或 none，nextOpeningAt 只在 scheduled 时有值。
type WebsiteVisitorReception struct {
	HandlerType      *WorkspaceIdentityType `json:"handlerType"`
	HandlerName      string                 `json:"handlerName"`
	HandlerAvatarURL string                 `json:"handlerAvatarUrl"`
	Online           bool                   `json:"online"`
	Reply            string                 `json:"reply"`
	NextOpeningAt    *time.Time             `json:"nextOpeningAt"`
}

// WebsiteVisitorConversation 定义网站访客会话摘要。
type WebsiteVisitorConversation struct {
	LastMessageSeq string `json:"lastMessageSeq"`
	ID             string `json:"id"`
	Title          string `json:"title"`
	// Preview 是末条消息的单行纯文本摘要。
	Preview        string                       `json:"preview"`
	LastMessageAt  time.Time                    `json:"lastMessageAt"`
	ServiceSession WebsiteVisitorServiceSession `json:"serviceSession"`
}

// WebsiteVisitorDirectory 定义网站访客当前渠道身份下的客户线程目录与新会话的接待状态；receptionRefreshAt 是接待状态随工作时间可能变化的下一时刻，访客端到时重新读取目录。
type WebsiteVisitorDirectory struct {
	Reception          WebsiteVisitorReception      `json:"reception"`
	ReceptionRefreshAt *time.Time                   `json:"receptionRefreshAt"`
	Conversations      []WebsiteVisitorConversation `json:"conversations"`
}

// WebsiteVisitorMessenger 定义网站 Messenger 初始化结果。
type WebsiteVisitorMessenger struct {
	VisitorToken       string                       `json:"visitorToken"`
	Reception          WebsiteVisitorReception      `json:"reception"`
	ReceptionRefreshAt *time.Time                   `json:"receptionRefreshAt"`
	Conversations      []WebsiteVisitorConversation `json:"conversations"`
}

// WebsiteVisitorTextMessageInput 定义网站访客文本发送参数。
type WebsiteVisitorTextMessageInput struct {
	ReplyToMessageID string  `json:"replyToMessageId"`
	ClientMessageID  string  `json:"clientMessageId"`
	ConversationID   *string `json:"conversationId"`
	Body             string  `json:"body"`
	// Page 是访客发送时所在的宿主页面，缺省时不更新访客上下文。
	Page *WebsiteVisitorPage `json:"page"`
}

// WebsiteVisitorTypingInput 定义网站访客在客户线程中的输入状态。
type WebsiteVisitorTypingInput struct {
	Active bool `json:"active"`
}

// WebsiteVisitorUploadInput 定义网站访客附件上传的文件元数据。
type WebsiteVisitorUploadInput struct {
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	ByteSize    int64  `json:"byteSize"`
}

// WebsiteVisitorUploadRequest 定义访客直传附件内容的请求。
type WebsiteVisitorUploadRequest struct {
	Headers map[string]string `json:"headers"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
}

// WebsiteVisitorUpload 定义访客附件上传结果。
type WebsiteVisitorUpload struct {
	FileID  string                      `json:"fileId"`
	Request WebsiteVisitorUploadRequest `json:"request"`
}

// WebsiteVisitorAttachmentMessageInput 定义网站访客附件发送参数。
type WebsiteVisitorAttachmentMessageInput struct {
	ReplyToMessageID string  `json:"replyToMessageId"`
	ClientMessageID  string  `json:"clientMessageId"`
	ConversationID   *string `json:"conversationId"`
	FileID           string  `json:"fileId"`
	Body             string  `json:"body"`
	ImageWidth       int     `json:"imageWidth"`
	ImageHeight      int     `json:"imageHeight"`
	// Page 是访客发送时所在的宿主页面，缺省时不更新访客上下文。
	Page *WebsiteVisitorPage `json:"page"`
}

// WebsiteVisitorAttachmentLinks 定义访客附件的预览与下载地址，内容未就绪时两者为空。
type WebsiteVisitorAttachmentLinks struct {
	// PreviewURL 只对浏览器可内嵌展示的图片返回。
	PreviewURL  string `json:"previewUrl"`
	DownloadURL string `json:"downloadUrl"`
}

// WebsiteVisitorAttachment 定义网站访客可见的消息附件。
type WebsiteVisitorAttachment struct {
	Name           string `json:"name"`
	ContentType    string `json:"contentType"`
	TransferStatus string `json:"transferStatus"`
	PreviewURL     string `json:"previewUrl"`
	DownloadURL    string `json:"downloadUrl"`
	ByteSize       int64  `json:"byteSize"`
	ImageWidth     int    `json:"imageWidth"`
	ImageHeight    int    `json:"imageHeight"`
}

// WebsiteVisitorMessageReference 定义网站访客可见的一层引用摘要。
type WebsiteVisitorMessageReference struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
	Author  string `json:"author,omitempty"`
	// Preview 是原文的单行纯文本摘要，原文已删除时为空。
	Preview string `json:"preview,omitempty"`
}

// WebsiteVisitorMessage 定义网站访客可见消息。
type WebsiteVisitorMessage struct {
	// ClientMessageID 仅向原发送身份返回。
	ClientMessageID    *string                         `json:"clientMessageId"`
	MessageSeq         string                          `json:"messageSeq"`
	ReplyTo            *WebsiteVisitorMessageReference `json:"replyTo"`
	Attachment         *WebsiteVisitorAttachment       `json:"attachment"`
	ID                 string                          `json:"id"`
	Author             string                          `json:"author"`
	Body               string                          `json:"body"`
	SenderIdentityType *WorkspaceIdentityType          `json:"senderIdentityType"`
	// Preview 是正文的单行纯文本摘要。
	Preview string `json:"preview"`
	// SenderIdentityID、SenderName 和 SenderAvatarURL 仅在企业成员或 AI 员工发送时有值。
	SenderIdentityID string `json:"senderIdentityId"`
	SenderName       string `json:"senderName"`
	SenderAvatarURL  string `json:"senderAvatarUrl"`
	// Event 仅在 Author 为 system 时有值。
	Event        *WebsiteVisitorEvent `json:"event"`
	OriginatedAt time.Time            `json:"originatedAt"`
	CreatedAt    time.Time            `json:"createdAt"`
}

// WebsiteVisitorEvent 定义访客时间线中的客服处理周期事件：member_joined、session_ended 或 email_collected，成员加入时带成员名称，留下邮箱时带邮箱。
type WebsiteVisitorEvent struct {
	Type             string `json:"type"`
	ServiceSessionID string `json:"serviceSessionId"`
	MemberName       string `json:"memberName"`
	Email            string `json:"email"`
	ToolCallID       string `json:"toolCallId"`
}

// WebsiteVisitorToolCall 定义 AI 员工提交给访客确认的操作：工具名称、AI 员工给出的参数与参数标题、当前状态与截止时间；decidable 为真时访客可以确认或拒绝。
type WebsiteVisitorToolCall struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Arguments      string            `json:"arguments"`
	ArgumentTitles map[string]string `json:"argumentTitles"`
	Status         string            `json:"status"`
	ExpiresAt      *time.Time        `json:"expiresAt"`
	Decidable      bool              `json:"decidable"`
}

// WebsiteVisitorReadInput 定义访客在客户线程中已读到的消息序号。
type WebsiteVisitorReadInput struct {
	MessageSeq string `json:"messageSeq"`
}

// WebsiteVisitorResumeInput 定义邮件「继续对话」链接携带的回访令牌。
type WebsiteVisitorResumeInput struct {
	Token string `json:"token"`
}

// WebsiteVisitorResume 定义回访令牌换得的访客令牌与要打开的客户线程摘要。
type WebsiteVisitorResume struct {
	VisitorToken string                     `json:"visitorToken"`
	Conversation WebsiteVisitorConversation `json:"conversation"`
}

// WebsiteVisitorSessionRating 定义客服处理周期的评价状态及其挂载的最近一次结束事件。
type WebsiteVisitorSessionRating struct {
	ServiceSessionID string `json:"serviceSessionId"`
	EndMessageID     string `json:"endMessageId"`
	WebsiteVisitorRating
}

// WebsiteVisitorRating 定义访客对客服处理周期的评价状态；rateable 为真时尚未评价。
type WebsiteVisitorRating struct {
	Rateable bool   `json:"rateable"`
	Resolved *bool  `json:"resolved"`
	Comment  string `json:"comment"`
}

// WebsiteVisitorRatingInput 定义访客提交的客服处理周期评价。
type WebsiteVisitorRatingInput struct {
	Resolved bool   `json:"resolved"`
	Comment  string `json:"comment"`
}

// WebsiteVisitorToolCallDecisionInput 定义网站访客对 AI 员工操作的裁决：Approve 为 true 表示确认，否则拒绝。
type WebsiteVisitorToolCallDecisionInput struct {
	Approve bool `json:"approve"`
}

// WebsiteVisitorMessageResult 定义网站访客消息写入结果。
type WebsiteVisitorMessageResult struct {
	Conversation            WebsiteVisitorConversation `json:"conversation"`
	CreatedConversation     bool                       `json:"createdConversation"`
	OpenedNewServiceSession bool                       `json:"openedNewServiceSession"`
	Message                 WebsiteVisitorMessage      `json:"message"`
}

// WebsiteVisitorMessageHistoryInput 定义网站访客历史分页参数。
type WebsiteVisitorMessageHistoryInput struct {
	Before string `query:"before"`
	After  string `query:"after"`
}

// WebsiteVisitorMessageHistory 定义网站访客历史分页结果。
type WebsiteVisitorMessageHistory struct {
	Messages []WebsiteVisitorMessage `json:"messages"`
	// SessionRatings 覆盖线程内全部已关闭或已评价的周期，ToolCalls 覆盖线程内全部提交给访客确认的操作，均与消息分页无关。
	SessionRatings []WebsiteVisitorSessionRating `json:"sessionRatings"`
	ToolCalls      []WebsiteVisitorToolCall      `json:"toolCalls"`
	Before         *string                       `json:"before"`
	After          *string                       `json:"after"`
}

// WebsiteVisitorHelpArticleSummary 定义帮助中心合集或搜索结果中的一篇文章。
type WebsiteVisitorHelpArticleSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// WebsiteVisitorHelpCollection 定义帮助中心的一个文章合集。
type WebsiteVisitorHelpCollection struct {
	ID          string                             `json:"id"`
	Name        string                             `json:"name"`
	Description string                             `json:"description"`
	Articles    []WebsiteVisitorHelpArticleSummary `json:"articles"`
}

// WebsiteVisitorHelpCenter 定义网站渠道帮助中心的文章合集，没有合集时访客端不显示帮助中心。
type WebsiteVisitorHelpCenter struct {
	Collections []WebsiteVisitorHelpCollection `json:"collections"`
}

// WebsiteVisitorHelpArticle 定义帮助中心文章详情，正文为 Markdown。
type WebsiteVisitorHelpArticle struct {
	ID             string    `json:"id"`
	CollectionID   string    `json:"collectionId"`
	CollectionName string    `json:"collectionName"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// WebsiteVisitorHelpSearchInput 定义访客在帮助中心输入的搜索内容。
type WebsiteVisitorHelpSearchInput struct {
	Query string `query:"q"`
}

// WebsiteVisitorHelpSearchResult 定义帮助中心搜索命中的文章，按相关度排序。
type WebsiteVisitorHelpSearchResult struct {
	Articles []WebsiteVisitorHelpArticleSummary `json:"articles"`
}

// WebsiteVisitorBackend 定义网站 Messenger 公开接口的业务契约。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [auth=public] [manual]
//
// appservicegen 按指令生成 net/http 路由与处理函数，路径相对服务端 /api。方法默认以访客身份调用，名为 externalID 的参数取自已授权访客的外部编号；
// auth=public 的方法无需访客身份；manual 标记的方法由 api 包手写路由。
type WebsiteVisitorBackend interface {
	// GetVisitorRealtimeConnection 换取当前访客的短期实时凭据和精确私有频道。
	//appservice:route GET /public/website-channels/{channelID}/realtime-connection
	GetVisitorRealtimeConnection(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string) (RealtimePeerConnection, error)
	// ListConversations 返回当前渠道身份的客户线程目录，供访客在初始化之后重新发现线程。
	//appservice:route GET /public/website-channels/{channelID}/conversations
	ListConversations(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string) (WebsiteVisitorDirectory, error)
	// SendTextMessage 持久化网站访客文本消息。
	//appservice:route POST /public/website-channels/{channelID}/messages
	SendTextMessage(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorTextMessageInput) (WebsiteVisitorMessageResult, error)
	// SendAttachmentMessage 持久化网站访客附件消息并激活上传文件。
	//appservice:route POST /public/website-channels/{channelID}/attachment-messages
	SendAttachmentMessage(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorAttachmentMessageInput) (WebsiteVisitorMessageResult, error)
	// CreateAttachmentUpload 创建网站访客附件的上传请求。
	//appservice:route POST /public/website-channels/{channelID}/attachments
	CreateAttachmentUpload(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorUploadInput) (WebsiteVisitorUpload, error)
	// CompleteAttachmentUpload 核验网站访客上传的附件内容。
	//appservice:route POST /public/website-channels/{channelID}/attachments/{fileID}
	CompleteAttachmentUpload(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, fileID string) error
	// GetMessageAttachment 重新签发网站访客消息附件的预览与下载地址。
	//appservice:route GET /public/website-channels/{channelID}/conversations/{conversationID}/messages/{messageID}/attachment
	GetMessageAttachment(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID, messageID string) (WebsiteVisitorAttachmentLinks, error)
	// ListMessages 返回网站访客指定客户线程的消息历史。
	//appservice:route GET /public/website-channels/{channelID}/conversations/{conversationID}/messages
	ListMessages(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorMessageHistoryInput) (WebsiteVisitorMessageHistory, error)
	// ReportTyping 向企业客服发布网站访客的输入状态。
	//appservice:route POST /public/website-channels/{channelID}/conversations/{conversationID}/typing
	ReportTyping(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorTypingInput) error
	// RateServiceSession 保存网站访客对已关闭客服处理周期的评价。
	//appservice:route POST /public/website-channels/{channelID}/conversations/{conversationID}/service-sessions/{serviceSessionID}/rating
	RateServiceSession(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID, serviceSessionID string, input WebsiteVisitorRatingInput) (WebsiteVisitorRating, error)
	// DecideToolCall 由网站访客确认或拒绝 AI 员工在其会话中提交的操作。
	//appservice:route POST /public/website-channels/{channelID}/conversations/{conversationID}/tool-calls/{toolCallID}/decision
	DecideToolCall(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID, toolCallID string, input WebsiteVisitorToolCallDecisionInput) error
	// MarkConversationRead 记录网站访客在客户线程中已读到的位置。
	//appservice:route POST /public/website-channels/{channelID}/conversations/{conversationID}/read
	MarkConversationRead(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorReadInput) error
	// ResumeVisitor 用邮件中的回访令牌恢复匿名访客身份，路由写入渠道访客 Cookie。
	//appservice:route POST /public/website-channels/{channelID}/resume auth=public manual
	ResumeVisitor(ctx context.Context, meta WebsiteVisitorMeta, channelID string, input WebsiteVisitorResumeInput) (WebsiteVisitorResume, error)
	// GetHelpCenter 返回网站渠道帮助中心的文章合集。
	//appservice:route GET /public/website-channels/{channelID}/help-center auth=public
	GetHelpCenter(ctx context.Context, meta WebsiteVisitorMeta, channelID string) (WebsiteVisitorHelpCenter, error)
	// GetHelpArticle 返回网站渠道帮助中心的文章详情。
	//appservice:route GET /public/website-channels/{channelID}/help-center/articles/{articleID} auth=public
	GetHelpArticle(ctx context.Context, meta WebsiteVisitorMeta, channelID, articleID string) (WebsiteVisitorHelpArticle, error)
	// SearchHelpCenter 在网站渠道帮助中心检索访客输入的内容，返回相关文章。
	//appservice:route GET /public/website-channels/{channelID}/help-center/search auth=public
	SearchHelpCenter(ctx context.Context, meta WebsiteVisitorMeta, channelID string, input WebsiteVisitorHelpSearchInput) (WebsiteVisitorHelpSearchResult, error)
}

// WebsiteVisitorService 定义网站 Messenger HTTP 适配器使用的业务调用：公开接口契约与签名身份校验。
type WebsiteVisitorService interface {
	WebsiteVisitorBackend
	// VerifyCustomer 按渠道所属企业的客户身份密钥校验签名身份。
	VerifyCustomer(ctx context.Context, meta WebsiteVisitorMeta, channelID, token string) (WebsiteVisitorCustomer, error)
}
