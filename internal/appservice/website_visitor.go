package appservice

import (
	"context"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	// WebsiteVisitorTokenHeader 是访客直传本地对象和调用公开接口使用的令牌请求头。
	WebsiteVisitorTokenHeader = "X-Visitor-Token"
	// WebsiteCustomerTokenHeader 是网站登录用户直传本地对象和调用公开接口携带签名身份的请求头。
	WebsiteCustomerTokenHeader = "X-Customer-Token"
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
	OrganizationID string
	UserID         string
	Name           string
	Email          string
	Profile        domain.WebsiteContactProfile
	ExpiresAt      time.Time
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
	HandlerType      *OrganizationIdentityType `json:"handlerType"`
	HandlerName      string                    `json:"handlerName"`
	HandlerAvatarURL string                    `json:"handlerAvatarUrl"`
	Online           bool                      `json:"online"`
	Reply            string                    `json:"reply"`
	NextOpeningAt    *time.Time                `json:"nextOpeningAt"`
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
	SenderIdentityType *OrganizationIdentityType       `json:"senderIdentityType"`
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

// WebsiteVisitorMessageResult 定义网站访客消息写入结果。
type WebsiteVisitorMessageResult struct {
	Conversation            WebsiteVisitorConversation `json:"conversation"`
	CreatedConversation     bool                       `json:"createdConversation"`
	OpenedNewServiceSession bool                       `json:"openedNewServiceSession"`
	Message                 WebsiteVisitorMessage      `json:"message"`
}

// WebsiteVisitorMessageHistoryInput 定义网站访客历史分页参数。
type WebsiteVisitorMessageHistoryInput struct {
	Before string
	After  string
}

// WebsiteVisitorMessageHistory 定义网站访客历史分页结果。
type WebsiteVisitorMessageHistory struct {
	Messages []WebsiteVisitorMessage `json:"messages"`
	// SessionRatings 覆盖线程内全部已关闭或已评价的周期，与消息分页无关。
	SessionRatings []WebsiteVisitorSessionRating `json:"sessionRatings"`
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
	Query string
}

// WebsiteVisitorHelpSearchResult 定义帮助中心搜索命中的文章，按相关度排序。
type WebsiteVisitorHelpSearchResult struct {
	Articles []WebsiteVisitorHelpArticleSummary `json:"articles"`
}

// WebsiteVisitorBackend 定义网站访客业务调用。
type WebsiteVisitorBackend interface {
	VerifyCustomer(context.Context, WebsiteVisitorMeta, string, string) (WebsiteVisitorCustomer, error)
	ListConversations(context.Context, WebsiteVisitorMeta, string, string) (WebsiteVisitorDirectory, error)
	SendTextMessage(context.Context, WebsiteVisitorMeta, string, string, WebsiteVisitorTextMessageInput) (WebsiteVisitorMessageResult, error)
	SendAttachmentMessage(context.Context, WebsiteVisitorMeta, string, string, WebsiteVisitorAttachmentMessageInput) (WebsiteVisitorMessageResult, error)
	CreateAttachmentUpload(context.Context, WebsiteVisitorMeta, string, string, WebsiteVisitorUploadInput) (WebsiteVisitorUpload, error)
	CompleteAttachmentUpload(context.Context, WebsiteVisitorMeta, string, string, string) error
	GetMessageAttachment(context.Context, WebsiteVisitorMeta, string, string, string, string) (WebsiteVisitorAttachmentLinks, error)
	ListMessages(context.Context, WebsiteVisitorMeta, string, string, string, WebsiteVisitorMessageHistoryInput) (WebsiteVisitorMessageHistory, error)
	ReportTyping(context.Context, WebsiteVisitorMeta, string, string, string, WebsiteVisitorTypingInput) error
	RateServiceSession(context.Context, WebsiteVisitorMeta, string, string, string, string, WebsiteVisitorRatingInput) (WebsiteVisitorRating, error)
	MarkConversationRead(context.Context, WebsiteVisitorMeta, string, string, string, WebsiteVisitorReadInput) error
	ResumeVisitor(context.Context, WebsiteVisitorMeta, string, WebsiteVisitorResumeInput) (WebsiteVisitorResume, error)
	GetHelpCenter(context.Context, WebsiteVisitorMeta, string) (WebsiteVisitorHelpCenter, error)
	GetHelpArticle(context.Context, WebsiteVisitorMeta, string, string) (WebsiteVisitorHelpArticle, error)
	SearchHelpCenter(context.Context, WebsiteVisitorMeta, string, WebsiteVisitorHelpSearchInput) (WebsiteVisitorHelpSearchResult, error)
}

// WebsiteVisitorService 转发网站访客业务调用。
type WebsiteVisitorService struct {
	backend WebsiteVisitorBackend
}

// NewWebsiteVisitorService 创建网站访客应用服务。
func NewWebsiteVisitorService(backend WebsiteVisitorBackend) *WebsiteVisitorService {
	return &WebsiteVisitorService{backend: backend}
}

// InitializeMessenger 返回访客 Token 和当前渠道的会话列表。
func (s *WebsiteVisitorService) InitializeMessenger(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, visitorToken string) (WebsiteVisitorMessenger, error) {
	directory, err := s.ListConversations(ctx, meta, channelID, externalID)
	if err != nil {
		return WebsiteVisitorMessenger{}, err
	}
	return WebsiteVisitorMessenger{VisitorToken: visitorToken, Reception: directory.Reception, ReceptionRefreshAt: directory.ReceptionRefreshAt, Conversations: directory.Conversations}, nil
}

// VerifyCustomer 按渠道所属企业的客户身份密钥校验签名身份。
func (s *WebsiteVisitorService) VerifyCustomer(ctx context.Context, meta WebsiteVisitorMeta, channelID, token string) (WebsiteVisitorCustomer, error) {
	return s.backend.VerifyCustomer(ctx, meta, channelID, token)
}

// ListConversations 返回当前渠道身份的客户线程目录，供访客在初始化之后重新发现线程。
func (s *WebsiteVisitorService) ListConversations(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string) (WebsiteVisitorDirectory, error) {
	return s.backend.ListConversations(ctx, meta, channelID, externalID)
}

// SendTextMessage 持久化网站访客文本消息。
func (s *WebsiteVisitorService) SendTextMessage(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorTextMessageInput) (WebsiteVisitorMessageResult, error) {
	return s.backend.SendTextMessage(ctx, meta, channelID, externalID, input)
}

// SendAttachmentMessage 持久化网站访客附件消息并激活上传文件。
func (s *WebsiteVisitorService) SendAttachmentMessage(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorAttachmentMessageInput) (WebsiteVisitorMessageResult, error) {
	return s.backend.SendAttachmentMessage(ctx, meta, channelID, externalID, input)
}

// CreateAttachmentUpload 创建网站访客附件的上传请求。
func (s *WebsiteVisitorService) CreateAttachmentUpload(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID string, input WebsiteVisitorUploadInput) (WebsiteVisitorUpload, error) {
	return s.backend.CreateAttachmentUpload(ctx, meta, channelID, externalID, input)
}

// CompleteAttachmentUpload 核验网站访客上传的附件内容。
func (s *WebsiteVisitorService) CompleteAttachmentUpload(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, fileID string) error {
	return s.backend.CompleteAttachmentUpload(ctx, meta, channelID, externalID, fileID)
}

// GetMessageAttachment 重新签发网站访客消息附件的预览与下载地址。
func (s *WebsiteVisitorService) GetMessageAttachment(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID, messageID string) (WebsiteVisitorAttachmentLinks, error) {
	return s.backend.GetMessageAttachment(ctx, meta, channelID, externalID, conversationID, messageID)
}

// ReportTyping 向企业客服发布网站访客的输入状态。
func (s *WebsiteVisitorService) ReportTyping(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorTypingInput) error {
	return s.backend.ReportTyping(ctx, meta, channelID, externalID, conversationID, input)
}

// ListMessages 返回网站访客指定客户线程的消息历史。
func (s *WebsiteVisitorService) ListMessages(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorMessageHistoryInput) (WebsiteVisitorMessageHistory, error) {
	return s.backend.ListMessages(ctx, meta, channelID, externalID, conversationID, input)
}

// RateServiceSession 保存网站访客对已关闭客服处理周期的评价。
func (s *WebsiteVisitorService) RateServiceSession(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID, serviceSessionID string, input WebsiteVisitorRatingInput) (WebsiteVisitorRating, error) {
	return s.backend.RateServiceSession(ctx, meta, channelID, externalID, conversationID, serviceSessionID, input)
}

// MarkConversationRead 记录网站访客在客户线程中已读到的位置。
func (s *WebsiteVisitorService) MarkConversationRead(ctx context.Context, meta WebsiteVisitorMeta, channelID, externalID, conversationID string, input WebsiteVisitorReadInput) error {
	return s.backend.MarkConversationRead(ctx, meta, channelID, externalID, conversationID, input)
}

// ResumeVisitor 用邮件中的回访令牌恢复匿名访客身份。
func (s *WebsiteVisitorService) ResumeVisitor(ctx context.Context, meta WebsiteVisitorMeta, channelID string, input WebsiteVisitorResumeInput) (WebsiteVisitorResume, error) {
	return s.backend.ResumeVisitor(ctx, meta, channelID, input)
}

// GetHelpCenter 返回网站渠道帮助中心的文章合集。
func (s *WebsiteVisitorService) GetHelpCenter(ctx context.Context, meta WebsiteVisitorMeta, channelID string) (WebsiteVisitorHelpCenter, error) {
	return s.backend.GetHelpCenter(ctx, meta, channelID)
}

// GetHelpArticle 返回网站渠道帮助中心的文章详情。
func (s *WebsiteVisitorService) GetHelpArticle(ctx context.Context, meta WebsiteVisitorMeta, channelID, articleID string) (WebsiteVisitorHelpArticle, error) {
	return s.backend.GetHelpArticle(ctx, meta, channelID, articleID)
}

// SearchHelpCenter 在网站渠道帮助中心检索访客输入的内容，返回相关文章。
func (s *WebsiteVisitorService) SearchHelpCenter(ctx context.Context, meta WebsiteVisitorMeta, channelID string, input WebsiteVisitorHelpSearchInput) (WebsiteVisitorHelpSearchResult, error) {
	return s.backend.SearchHelpCenter(ctx, meta, channelID, input)
}
