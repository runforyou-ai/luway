//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	helpcenteraction "github.com/runforyou-ai/luway/internal/actions/helpcenter"
	"github.com/runforyou-ai/luway/internal/actions/ratelimit"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

var _ appservice.WebsiteVisitorService = (*WebsiteVisitorBackend)(nil)

// WebsiteVisitorAudience 是网站访客事件流的受众标识，渠道身份记录 ID 用于构造受众 Subject。
type WebsiteVisitorAudience struct {
	WorkspaceID       string
	ChannelID         string
	ChannelIdentityID string
}

// WebsiteVisitorBackend 在服务端进程内调用匿名访客 Action 和 Query。
type WebsiteVisitorBackend struct {
	db                *bun.DB
	realtimePrefix    string
	listConversations *customerchataction.ListWebsiteConversationsQuery
	sendMessage       *customerchataction.ReceiveWebsiteCustomerMessageAction
	listMessages      *customerchataction.ListWebsiteMessagesQuery
	authorizeVisitor  *customerchataction.AuthorizeWebsiteVisitorQuery
	verifyCustomer    *customerchataction.VerifyWebsiteCustomerQuery
	createUpload      *customerchataction.CreateWebsiteVisitorUploadAction
	completeUpload    *customerchataction.CompleteWebsiteVisitorUploadAction
	getAttachment     *customerchataction.GetWebsiteVisitorAttachmentQuery
	reportTyping      *customerchataction.ReportWebsiteVisitorTypingAction
	rateSession       *customerchataction.RateWebsiteServiceSessionAction
	decideToolCall    *customerchataction.DecideWebsiteToolCallAction
	markRead          *customerchataction.MarkWebsiteConversationReadAction
	resumeVisitor     *customerchataction.ResumeWebsiteVisitorQuery
	getHelpCenter     *helpcenteraction.GetHelpCenterQuery
	getHelpArticle    *helpcenteraction.GetArticleQuery
	searchHelpCenter  *helpcenteraction.SearchQuery
	localFiles        *serverfilecontent.LocalStore
	s3                serverfilecontent.S3Settings
	links             serverfilecontent.Links
	limiter           *ratelimit.Limiter
	reader            *serverfilecontent.Reader
}

// NewWebsiteVisitorBackend 创建匿名网站访客直接后端；s3 返回部署当前的对象存储配置，emailSender 按部署当前的邮件配置发送，knowledgeRetrieval 用于帮助中心搜索。
func NewWebsiteVisitorBackend(db *bun.DB, agentScheduler conversationaction.CustomerAgentMessageScheduler, taskEnqueuer servertask.TxEnqueuer, localFiles *serverfilecontent.LocalStore, s3 serverfilecontent.S3Settings, emailSender customernotify.Sender, knowledgeRetrieval helpcenteraction.Retrieval) *WebsiteVisitorBackend {
	backend := &WebsiteVisitorBackend{
		db: db, realtimePrefix: "app_realtime",
		listConversations: customerchataction.NewListWebsiteConversationsQuery(db),
		sendMessage:       customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentScheduler, taskEnqueuer, emailSender),
		listMessages:      customerchataction.NewListWebsiteMessagesQuery(db),
		authorizeVisitor:  customerchataction.NewAuthorizeWebsiteVisitorQuery(db),
		verifyCustomer:    customerchataction.NewVerifyWebsiteCustomerQuery(db),
		completeUpload:    customerchataction.NewCompleteWebsiteVisitorUploadAction(db),
		getAttachment:     customerchataction.NewGetWebsiteVisitorAttachmentQuery(db),
		reportTyping:      customerchataction.NewReportWebsiteVisitorTypingAction(db),
		rateSession:       customerchataction.NewRateWebsiteServiceSessionAction(db, taskEnqueuer),
		decideToolCall:    customerchataction.NewDecideWebsiteToolCallAction(db, tooldecisionaction.New(db, taskEnqueuer, agentrunaction.NewRunScopes(taskEnqueuer))),
		markRead:          customerchataction.NewMarkWebsiteConversationReadAction(db),
		resumeVisitor:     customerchataction.NewResumeWebsiteVisitorQuery(db),
		getHelpCenter:     helpcenteraction.NewGetHelpCenterQuery(db),
		getHelpArticle:    helpcenteraction.NewGetArticleQuery(db),
		searchHelpCenter:  helpcenteraction.NewSearchQuery(db, knowledgeRetrieval),
		localFiles:        localFiles,
		s3:                s3,
		links:             serverfilecontent.NewLinks(nil, s3),
		limiter:           ratelimit.NewLimiter(db),
		reader:            serverfilecontent.NewReader(localFiles, s3),
	}
	backend.createUpload = customerchataction.NewCreateWebsiteVisitorUploadAction(db, taskEnqueuer, func() domain.FileStorageBackend { return s3().Backend() })
	return backend
}

// AuthenticateVisitor 解析访客实时频道主体；渠道停用或尚未建立业务身份时返回与访客 HTTP 接口一致的错误。
func (b *WebsiteVisitorBackend) AuthenticateVisitor(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string) (_ WebsiteVisitorAudience, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.AuthenticateVisitor")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	audience, err := b.authorizeVisitor.Execute(ctx, channelID, externalID)
	if err != nil {
		return WebsiteVisitorAudience{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	return WebsiteVisitorAudience{WorkspaceID: audience.WorkspaceID, ChannelID: audience.ChannelID, ChannelIdentityID: audience.ChannelIdentityID}, nil
}

// ListConversations 返回网站访客的客户会话目录与渠道新会话的接待状态。
func (b *WebsiteVisitorBackend) ListConversations(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string) (_ appservice.WebsiteVisitorDirectory, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.ListConversations")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	directory, err := b.listConversations.Execute(ctx, channelID, externalID)
	if err != nil {
		return appservice.WebsiteVisitorDirectory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	reception, err := websiteVisitorReceptionFromAction(directory.NewSessionReception, b.links)
	if err != nil {
		return appservice.WebsiteVisitorDirectory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	conversations, err := arr.MapErr(directory.Conversations, func(item customerchataction.ConversationSummary) (appservice.WebsiteVisitorConversation, error) {
		return websiteVisitorConversationFromAction(item, b.links)
	})
	if err != nil {
		return appservice.WebsiteVisitorDirectory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	return appservice.WebsiteVisitorDirectory{Reception: reception, ReceptionRefreshAt: directory.ReceptionRefreshAt, Conversations: arr.OrEmpty(conversations)}, nil
}

// VerifyCustomer 按渠道所属企业的客户身份密钥校验签名身份。
func (b *WebsiteVisitorBackend) VerifyCustomer(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, token string) (_ appservice.WebsiteVisitorCustomer, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.VerifyCustomer")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	verified, err := b.verifyCustomer.Execute(ctx, channelID, token)
	if err != nil {
		return appservice.WebsiteVisitorCustomer{}, websiteVisitorError(meta, err, i18n.MessengerIdentityExpired)
	}
	return websiteVisitorCustomerFromAction(verified), nil
}

// websiteVisitorCustomerFromAction 转换验签通过的网站登录用户。
func websiteVisitorCustomerFromAction(value customerchataction.VerifiedWebsiteCustomer) appservice.WebsiteVisitorCustomer {
	return appservice.WebsiteVisitorCustomer{
		WorkspaceID: value.WorkspaceID, UserID: value.Customer.UserID, Name: value.Customer.Name,
		Email: value.Customer.Email, Profile: value.Customer.Profile, ExpiresAt: value.ExpiresAt,
	}
}

// websiteCustomerInput 返回访客元信息中已验证的登录用户，匿名访客返回空。
func websiteCustomerInput(meta appservice.WebsiteVisitorMeta) *customerchataction.SignedCustomer {
	if meta.Customer == nil {
		return nil
	}
	return &customerchataction.SignedCustomer{UserID: meta.Customer.UserID, Name: meta.Customer.Name, Email: meta.Customer.Email, Profile: meta.Customer.Profile}
}

// SendTextMessage 持久化网站访客文本消息。
func (b *WebsiteVisitorBackend) SendTextMessage(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string, input appservice.WebsiteVisitorTextMessageInput) (_ appservice.WebsiteVisitorMessageResult, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.SendTextMessage")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	if err := b.allow(ctx, meta, ratelimit.VisitorMessageByIdentity.For(channelID, externalID), ratelimit.VisitorMessageByIP.For(channelID, ratelimit.IPSubject(appservice.ClientIP(ctx)))); err != nil {
		return appservice.WebsiteVisitorMessageResult{}, err
	}
	result, err := b.sendMessage.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channelID, ExternalID: externalID, ConversationID: input.ConversationID,
		ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
		Customer: websiteCustomerInput(meta), VisitorContext: websiteVisitorContext(meta, input.Page),
	})
	if err != nil {
		return appservice.WebsiteVisitorMessageResult{}, websiteVisitorError(meta, err, i18n.VisitorErrorSendFailed)
	}
	return b.sentMessageResult(ctx, meta, channelID, result)
}

// SendAttachmentMessage 持久化网站访客附件消息并激活上传文件。
func (b *WebsiteVisitorBackend) SendAttachmentMessage(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string, input appservice.WebsiteVisitorAttachmentMessageInput) (_ appservice.WebsiteVisitorMessageResult, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.SendAttachmentMessage")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	if err := b.allow(ctx, meta, ratelimit.VisitorMessageByIdentity.For(channelID, externalID), ratelimit.VisitorMessageByIP.For(channelID, ratelimit.IPSubject(appservice.ClientIP(ctx)))); err != nil {
		return appservice.WebsiteVisitorMessageResult{}, err
	}
	result, err := b.sendMessage.ExecuteAttachment(ctx, customerchataction.WebsiteCustomerAttachmentMessageInput{
		ChannelID: channelID, ExternalID: externalID, ConversationID: input.ConversationID,
		ClientMessageID: input.ClientMessageID, FileID: input.FileID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
		ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight,
		Customer: websiteCustomerInput(meta), VisitorContext: websiteVisitorContext(meta, input.Page),
	})
	if err != nil {
		return appservice.WebsiteVisitorMessageResult{}, websiteVisitorError(meta, err, i18n.VisitorErrorSendFailed)
	}
	return b.sentMessageResult(ctx, meta, channelID, result)
}

// sentMessageResult 转换访客消息写入结果并记录保存日志。
func (b *WebsiteVisitorBackend) sentMessageResult(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID string, result customerchataction.ReceiveWebsiteCustomerMessageResult) (appservice.WebsiteVisitorMessageResult, error) {
	linker := visitorAttachmentLinker{s3: b.s3, fileLinks: b.links}
	message, err := websiteVisitorMessageFromAction(ctx, &linker, result.Message)
	if err != nil {
		return appservice.WebsiteVisitorMessageResult{}, websiteVisitorError(meta, err, i18n.VisitorErrorSendFailed)
	}
	conversation, err := websiteVisitorConversationFromAction(result.Conversation, b.links)
	if err != nil {
		return appservice.WebsiteVisitorMessageResult{}, websiteVisitorError(meta, err, i18n.VisitorErrorSendFailed)
	}
	slog.InfoContext(ctx, "网站访客消息已保存",
		"channel_id", channelID,
		"conversation_id", result.Conversation.ID,
		"service_session_id", result.Conversation.ServiceSessionID,
		"message_id", result.Message.ID,
		"created_conversation", result.CreatedConversation,
		"opened_new_service_session", result.OpenedNewServiceSession,
	)
	return appservice.WebsiteVisitorMessageResult{
		Conversation:            conversation,
		CreatedConversation:     result.CreatedConversation,
		OpenedNewServiceSession: result.OpenedNewServiceSession,
		Message:                 message,
	}, nil
}

// CreateAttachmentUpload 创建网站访客附件的上传请求。
func (b *WebsiteVisitorBackend) CreateAttachmentUpload(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID string, input appservice.WebsiteVisitorUploadInput) (_ appservice.WebsiteVisitorUpload, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.CreateAttachmentUpload")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	if err := b.allow(ctx, meta, ratelimit.VisitorUploadByIdentity.For(channelID, externalID), ratelimit.VisitorUploadByIP.For(channelID, ratelimit.IPSubject(appservice.ClientIP(ctx)))); err != nil {
		return appservice.WebsiteVisitorUpload{}, err
	}
	record, err := b.createUpload.Execute(ctx, customerchataction.WebsiteVisitorUploadInput{
		ChannelID: channelID, ExternalID: externalID,
		FileName: input.FileName, ContentType: input.ContentType, ByteSize: input.ByteSize,
		Customer: websiteCustomerInput(meta),
	})
	if err != nil {
		return appservice.WebsiteVisitorUpload{}, websiteVisitorError(meta, err, i18n.VisitorErrorUploadFailed)
	}
	request, err := b.visitorUploadRequest(ctx, meta, record)
	if err != nil {
		return appservice.WebsiteVisitorUpload{}, websiteVisitorError(meta, err, i18n.VisitorErrorUploadFailed)
	}
	return appservice.WebsiteVisitorUpload{FileID: record.ID, Request: request}, nil
}

// CompleteAttachmentUpload 核验网站访客上传的附件内容。
func (b *WebsiteVisitorBackend) CompleteAttachmentUpload(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, fileID string) (err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.CompleteAttachmentUpload")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	record, err := b.completeUpload.Execute(ctx, channelID, externalID, fileID, b.reader.Inspect)
	if err != nil {
		return websiteVisitorError(meta, err, i18n.VisitorErrorUploadFailed)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, record.WorkspaceID), "网站访客附件上传已完成", "channel_id", channelID, "file_id", record.ID, "storage_backend", record.StorageBackend)
	return nil
}

// GetMessageAttachment 重新签发网站访客消息附件的预览与下载地址。
func (b *WebsiteVisitorBackend) GetMessageAttachment(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID, messageID string) (_ appservice.WebsiteVisitorAttachmentLinks, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.GetMessageAttachment")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	record, err := b.getAttachment.Execute(ctx, channelID, externalID, conversationID, messageID)
	if err != nil {
		return appservice.WebsiteVisitorAttachmentLinks{}, websiteVisitorError(meta, err, i18n.MessengerAttachmentUnavailable)
	}
	linker := visitorAttachmentLinker{s3: b.s3, fileLinks: b.links}
	links, err := linker.links(ctx, domain.FileStorageBackend(record.StorageBackend), record.StorageKey, record.OriginalName, record.ContentType)
	if err != nil {
		return appservice.WebsiteVisitorAttachmentLinks{}, websiteVisitorError(meta, err, i18n.MessengerAttachmentUnavailable)
	}
	return links, nil
}

// visitorUploadRequest 返回访客直传的本地上传地址或 S3 预签名请求。
func (b *WebsiteVisitorBackend) visitorUploadRequest(ctx context.Context, meta appservice.WebsiteVisitorMeta, record *servermodels.File) (appservice.WebsiteVisitorUploadRequest, error) {
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		contentURL, err := b.links.URL(domain.FileStorageBackendLocal, record.StorageKey)
		if err != nil {
			return appservice.WebsiteVisitorUploadRequest{}, err
		}
		// 登录用户以签名身份直传，匿名访客以访客令牌直传。
		headers := map[string]string{appservice.WebsiteVisitorTokenHeader: meta.Token, "Content-Type": record.ContentType}
		if meta.Customer != nil {
			headers = map[string]string{appservice.CustomerTokenHeader: meta.CustomerToken, "Content-Type": record.ContentType}
		}
		return appservice.WebsiteVisitorUploadRequest{Method: http.MethodPut, URL: contentURL, Headers: headers}, nil
	}
	signed, err := serverfilecontent.PresignPut(ctx, b.s3(), record.StorageKey, record.ContentType, record.ByteSize)
	if err != nil {
		return appservice.WebsiteVisitorUploadRequest{}, err
	}
	return appservice.WebsiteVisitorUploadRequest{Method: signed.Method, URL: signed.URL, Headers: signed.Headers}, nil
}

// visitorAttachmentLinker 按部署级对象存储配置签发访客附件地址。
type visitorAttachmentLinker struct {
	s3        serverfilecontent.S3Settings
	fileLinks serverfilecontent.Links
}

// links 返回附件的下载地址，可内嵌展示的图片同时返回预览地址。
func (l *visitorAttachmentLinker) links(ctx context.Context, backend domain.FileStorageBackend, storageKey, fileName, contentType string) (appservice.WebsiteVisitorAttachmentLinks, error) {
	_, inline := domain.InlineImageExtension(contentType)
	if backend == domain.FileStorageBackendLocal {
		contentURL, err := l.fileLinks.URL(domain.FileStorageBackendLocal, storageKey)
		if err != nil {
			return appservice.WebsiteVisitorAttachmentLinks{}, err
		}
		links := appservice.WebsiteVisitorAttachmentLinks{DownloadURL: contentURL + "?download=" + url.QueryEscape(fileName)}
		if inline {
			links.PreviewURL = contentURL
		}
		return links, nil
	}
	download, err := serverfilecontent.PresignDownload(ctx, l.s3(), storageKey, mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	if err != nil {
		return appservice.WebsiteVisitorAttachmentLinks{}, err
	}
	links := appservice.WebsiteVisitorAttachmentLinks{DownloadURL: download.URL}
	if inline {
		preview, err := serverfilecontent.PresignDownload(ctx, l.s3(), storageKey, "inline")
		if err != nil {
			return appservice.WebsiteVisitorAttachmentLinks{}, err
		}
		links.PreviewURL = preview.URL
	}
	return links, nil
}

// avatarURL 返回头像文件的稳定公开地址。
func (l *visitorAttachmentLinker) avatarURL(_ context.Context, location customerchataction.FileLocation) (string, error) {
	return l.fileLinks.URL(location.StorageBackend, location.StorageKey)
}

// ListMessages 返回网站访客指定客户线程的消息历史。
func (b *WebsiteVisitorBackend) ListMessages(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID string, input appservice.WebsiteVisitorMessageHistoryInput) (_ appservice.WebsiteVisitorMessageHistory, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.ListMessages")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	before, after, err := decodeMessageCursors(conversationID, input.Before, input.After)
	if err != nil {
		return appservice.WebsiteVisitorMessageHistory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	page, err := b.listMessages.Execute(ctx, customerchataction.MessageHistoryInput{
		ChannelID: channelID, ExternalID: externalID, ConversationID: conversationID, Before: before, After: after,
	})
	if err != nil {
		return appservice.WebsiteVisitorMessageHistory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	linker := visitorAttachmentLinker{s3: b.s3, fileLinks: b.links}
	messages, err := arr.MapErr(page.Messages, func(message customerchataction.Message) (appservice.WebsiteVisitorMessage, error) {
		return websiteVisitorMessageFromAction(ctx, &linker, message)
	})
	if err != nil {
		return appservice.WebsiteVisitorMessageHistory{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	return appservice.WebsiteVisitorMessageHistory{
		Messages: arr.OrEmpty(messages),
		SessionRatings: arr.OrEmpty(arr.Map(page.SessionRatings, func(rating customerchataction.VisitorSessionRating) appservice.WebsiteVisitorSessionRating {
			return appservice.WebsiteVisitorSessionRating{
				ServiceSessionID: rating.ServiceSessionID, EndMessageID: rating.EndMessageID, WebsiteVisitorRating: websiteVisitorRatingFromAction(rating.VisitorRating),
			}
		})),
		ToolCalls: arr.OrEmpty(arr.Map(page.ToolCalls, func(call customerchataction.VisitorToolCall) appservice.WebsiteVisitorToolCall {
			return appservice.WebsiteVisitorToolCall{
				ID: call.ID, Name: call.Name, Arguments: call.Arguments, ArgumentTitles: call.ArgumentTitles,
				Status: string(call.Status), ExpiresAt: call.ExpiresAt, Decidable: call.Decidable,
			}
		})),
		Before: encodeConversationMessageCursor(conversationID, page.Before),
		After:  encodeConversationMessageCursor(conversationID, page.After),
	}, nil
}

// ReportTyping 向企业客服发布网站访客在客户线程中的输入状态。
func (b *WebsiteVisitorBackend) ReportTyping(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID string, input appservice.WebsiteVisitorTypingInput) (err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.ReportTyping")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	if err := b.reportTyping.Execute(ctx, channelID, externalID, conversationID, input.Active); err != nil {
		return websiteVisitorError(meta, err, i18n.MessengerRequestFailed)
	}
	return nil
}

// RateServiceSession 保存网站访客对已关闭客服处理周期的评价。
func (b *WebsiteVisitorBackend) RateServiceSession(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID, serviceSessionID string, input appservice.WebsiteVisitorRatingInput) (_ appservice.WebsiteVisitorRating, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.RateServiceSession")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	rating, err := b.rateSession.Execute(ctx, customerchataction.WebsiteServiceSessionRatingInput{
		ChannelID: channelID, ExternalID: externalID, ConversationID: conversationID, ServiceSessionID: serviceSessionID,
		Resolved: input.Resolved, Comment: input.Comment,
	})
	if err != nil {
		return appservice.WebsiteVisitorRating{}, websiteVisitorError(meta, err, i18n.VisitorErrorRateFailed)
	}
	slog.InfoContext(ctx, "网站访客已评价客服处理周期", "channel_id", channelID, "conversation_id", conversationID, "service_session_id", serviceSessionID, "resolved", input.Resolved)
	return websiteVisitorRatingFromAction(rating), nil
}

// DecideToolCall 由网站访客确认或拒绝 AI 员工在其会话中提交的操作。
func (b *WebsiteVisitorBackend) DecideToolCall(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID, toolCallID string, input appservice.WebsiteVisitorToolCallDecisionInput) (err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.DecideToolCall")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	err = b.decideToolCall.Execute(ctx, customerchataction.WebsiteToolCallDecisionInput{
		ChannelID: channelID, ExternalID: externalID, ConversationID: conversationID, ToolCallID: toolCallID, Approve: input.Approve,
	})
	switch {
	case err == nil:
		slog.InfoContext(ctx, "网站访客已裁决 AI 员工操作", "channel_id", channelID, "conversation_id", conversationID, "tool_call_id", toolCallID, "approve", input.Approve)
		return nil
	case errors.Is(err, tooldecisionaction.ErrToolCallUnavailable):
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindNotFound, i18n.VisitorErrorToolCallUnavailable, nil)
	case errors.Is(err, tooldecisionaction.ErrToolCallDecided):
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindConflict, i18n.VisitorErrorToolCallDecided, nil).WithReason("tool_call_decided")
	}
	return websiteVisitorError(meta, err, i18n.VisitorErrorToolCallDecisionFailed)
}

// MarkConversationRead 记录网站访客在客户线程中已读到的位置。
func (b *WebsiteVisitorBackend) MarkConversationRead(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, externalID, conversationID string, input appservice.WebsiteVisitorReadInput) (err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.MarkConversationRead")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	messageSeq, err := strconv.ParseInt(input.MessageSeq, 10, 64)
	if err != nil {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil)
	}
	if err := b.markRead.Execute(ctx, channelID, externalID, conversationID, messageSeq); err != nil {
		return websiteVisitorError(meta, err, i18n.MessengerRequestFailed)
	}
	return nil
}

// ResumeVisitor 用邮件中的回访令牌恢复匿名访客身份。
func (b *WebsiteVisitorBackend) ResumeVisitor(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID string, input appservice.WebsiteVisitorResumeInput) (_ appservice.WebsiteVisitorResume, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.ResumeVisitor")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	resumed, err := b.resumeVisitor.Execute(ctx, channelID, input.Token)
	if err != nil {
		return appservice.WebsiteVisitorResume{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	conversation, err := websiteVisitorConversationFromAction(resumed.Conversation, b.links)
	if err != nil {
		return appservice.WebsiteVisitorResume{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	return appservice.WebsiteVisitorResume{VisitorToken: resumed.VisitorToken, Conversation: conversation}, nil
}

// GetHelpCenter 返回网站渠道帮助中心的文章合集。
func (b *WebsiteVisitorBackend) GetHelpCenter(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID string) (_ appservice.WebsiteVisitorHelpCenter, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.GetHelpCenter")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	collections, err := b.getHelpCenter.Execute(ctx, channelID)
	if err != nil {
		return appservice.WebsiteVisitorHelpCenter{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	result := appservice.WebsiteVisitorHelpCenter{Collections: make([]appservice.WebsiteVisitorHelpCollection, 0, len(collections))}
	for _, collection := range collections {
		result.Collections = append(result.Collections, appservice.WebsiteVisitorHelpCollection{
			ID: collection.ID, Name: collection.Name, Description: collection.Description,
			Articles: websiteVisitorHelpArticles(collection.Articles),
		})
	}
	return result, nil
}

// GetHelpArticle 返回网站渠道帮助中心的文章详情。
func (b *WebsiteVisitorBackend) GetHelpArticle(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID, articleID string) (_ appservice.WebsiteVisitorHelpArticle, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.GetHelpArticle")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	article, err := b.getHelpArticle.Execute(ctx, channelID, articleID)
	if err != nil {
		return appservice.WebsiteVisitorHelpArticle{}, websiteVisitorError(meta, err, i18n.VisitorErrorLoadFailed)
	}
	return appservice.WebsiteVisitorHelpArticle{
		ID: article.ID, CollectionID: article.CollectionID, CollectionName: article.CollectionName,
		Title: article.Title, Body: article.Body, UpdatedAt: article.UpdatedAt,
	}, nil
}

// SearchHelpCenter 在网站渠道帮助中心检索访客输入的内容，返回相关文章。
func (b *WebsiteVisitorBackend) SearchHelpCenter(ctx context.Context, meta appservice.WebsiteVisitorMeta, channelID string, input appservice.WebsiteVisitorHelpSearchInput) (_ appservice.WebsiteVisitorHelpSearchResult, err error) {
	ctx = logscope.WithOperation(ctx, "WebsiteVisitor.SearchHelpCenter")
	defer dispatch.Settle(&ctx, &err, visitorInternalError(meta))
	if err := b.allow(ctx, meta, ratelimit.HelpSearchByIP.For(channelID, ratelimit.IPSubject(appservice.ClientIP(ctx))), ratelimit.HelpSearchByChannel.For(channelID)); err != nil {
		return appservice.WebsiteVisitorHelpSearchResult{}, err
	}
	articles, err := b.searchHelpCenter.Execute(ctx, channelID, input.Query)
	if err != nil {
		return appservice.WebsiteVisitorHelpSearchResult{}, websiteVisitorError(meta, err, i18n.MessengerHelpSearchFailed)
	}
	return appservice.WebsiteVisitorHelpSearchResult{Articles: websiteVisitorHelpArticles(articles)}, nil
}

// allow 为访客请求消耗限速额度，超出时返回按对客语言本地化的限速错误。
func (b *WebsiteVisitorBackend) allow(ctx context.Context, meta appservice.WebsiteVisitorMeta, checks ...ratelimit.Check) error {
	err := b.limiter.Allow(ctx, checks...)
	if limited, ok := errors.AsType[*ratelimit.LimitedError](err); ok {
		return appservice.WebsiteVisitorRateLimitedError(meta.Locale, limited.RetryAfter)
	}
	return err
}

// websiteVisitorHelpArticles 转换帮助中心文章摘要。
func websiteVisitorHelpArticles(articles []helpcenteraction.ArticleSummary) []appservice.WebsiteVisitorHelpArticleSummary {
	return arr.OrEmpty(arr.Map(articles, func(article helpcenteraction.ArticleSummary) appservice.WebsiteVisitorHelpArticleSummary {
		return appservice.WebsiteVisitorHelpArticleSummary{ID: article.ID, Title: article.Title}
	}))
}

// websiteVisitorError 把语言无关访客错误映射为按对客语言本地化的应用错误。
func websiteVisitorError(meta appservice.WebsiteVisitorMeta, err error, failureKey i18n.Key) error {
	if validation, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		fieldKeys := dispatch.TranslateFields(validation.Fields, websiteVisitorValidationKeys)
		// 各字段文案一致时直接作为错误文案，否则提示请求无效。
		var messageKey i18n.Key
		for _, key := range fieldKeys {
			if messageKey != "" && messageKey != key {
				messageKey = i18n.VisitorErrorRequestInvalid
				break
			}
			messageKey = key
		}
		if messageKey == "" {
			messageKey = i18n.VisitorErrorRequestInvalid
		}
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindInvalid, messageKey, fieldKeys)
	}
	if errors.Is(err, customerchataction.ErrChannelNotFound) || errors.Is(err, helpcenteraction.ErrChannelNotFound) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindNotFound, i18n.VisitorErrorChatUnavailable, nil)
	}
	if errors.Is(err, helpcenteraction.ErrArticleNotFound) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindNotFound, i18n.MessengerHelpArticleUnavailable, nil)
	}
	if errors.Is(err, helpcenteraction.ErrQueryInvalid) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil)
	}
	if errors.Is(err, customerchataction.ErrCustomerIdentityInvalid) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindInvalid, i18n.MessengerIdentityExpired, nil).WithReason(appservice.WebsiteCustomerIdentityInvalidReason).WithStatus(http.StatusUnauthorized)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindNotFound, i18n.VisitorErrorConversationNotFound, nil)
	}
	if errors.Is(err, fileaction.ErrFileNotFound) {
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindNotFound, i18n.MessengerAttachmentUnavailable, nil)
	}
	if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		messageKey := i18n.VisitorErrorMessageConflict
		switch conflict.Reason {
		case conversationaction.ConflictReasonReplyTargetInvalid:
			messageKey = i18n.VisitorErrorReplyTargetInvalid
		case conversationaction.ConflictReasonAttachmentTooLarge:
			messageKey = i18n.VisitorErrorAttachmentTooLarge
		case customerchataction.ConflictReasonAttachmentsDisabled:
			messageKey = i18n.VisitorErrorAttachmentsDisabled
		case channelinbound.ConflictReasonUnrepliedConversationsExceeded:
			messageKey = i18n.VisitorErrorUnrepliedConversationsExceeded
		case customerchataction.ConflictReasonServiceSessionNotRateable:
			messageKey = i18n.VisitorErrorRatingUnavailable
		}
		return appservice.WebsiteVisitorError(meta.Locale, appservice.ErrorKindConflict, messageKey, nil).WithReason(conflict.Reason)
	}
	return appservice.WebsiteVisitorFailedError(meta.Locale, failureKey, err)
}

// websiteVisitorValidationKeys 把网站访客消息输入校验错误码映射为本地化文案键。
var websiteVisitorValidationKeys = map[conversationaction.ValidationCode]i18n.Key{
	conversationaction.ValidationReplyToMessageIDInvalid: i18n.VisitorErrorRequestInvalid,
	customerchataction.ValidationChannelIDInvalid:        i18n.VisitorErrorRequestInvalid,
	customerchataction.ValidationExternalIDInvalid:       i18n.VisitorErrorRequestInvalid,
	conversationaction.ValidationConversationIDInvalid:   i18n.VisitorErrorRequestInvalid,
	conversationaction.ValidationClientMessageIDInvalid:  i18n.VisitorErrorRequestInvalid,
	conversationaction.ValidationBodyRequired:            i18n.VisitorErrorMessageRequired,
	conversationaction.ValidationBodyTooLong:             i18n.VisitorErrorMessageTooLong,
	conversationaction.ValidationCursorInvalid:           i18n.VisitorErrorRequestInvalid,
	customerchataction.ValidationRatingCommentTooLong:    i18n.VisitorErrorRatingCommentTooLong,
	conversationaction.ValidationFileIDInvalid:           i18n.MessengerAttachmentUnavailable,
	fileaction.ValidationFileNameRequired:                i18n.VisitorErrorFileInvalid,
	fileaction.ValidationContentTypeInvalid:              i18n.VisitorErrorFileInvalid,
	fileaction.ValidationByteSizeInvalid:                 i18n.VisitorErrorFileInvalid,
	fileaction.ValidationPurposeInvalid:                  i18n.VisitorErrorRequestInvalid,
}

// websiteVisitorConversationFromAction 转换访客会话摘要及其当前接待状态。
func websiteVisitorConversationFromAction(value customerchataction.ConversationSummary, links serverfilecontent.Links) (appservice.WebsiteVisitorConversation, error) {
	reception, err := websiteVisitorReceptionFromAction(value.Reception, links)
	if err != nil {
		return appservice.WebsiteVisitorConversation{}, err
	}
	return appservice.WebsiteVisitorConversation{
		ID: value.ID, Title: value.Title, Preview: *messagePreviewText(&value.Preview, value.PreviewSenderIdentityType), LastMessageSeq: strconv.FormatInt(value.LastMessageSeq, 10), LastMessageAt: value.LastMessageAt,
		ServiceSession: appservice.WebsiteVisitorServiceSession{ID: value.ServiceSessionID, Status: string(value.ServiceSessionStatus), Reception: reception},
	}, nil
}

// websiteVisitorReceptionFromAction 转换访客端接待状态，接待方头像签为公开地址。
func websiteVisitorReceptionFromAction(value serviceroute.Reception, links serverfilecontent.Links) (appservice.WebsiteVisitorReception, error) {
	reception := appservice.WebsiteVisitorReception{
		HandlerType: value.HandlerType, HandlerName: value.HandlerName,
		Online: value.Online, Reply: string(value.Reply), NextOpeningAt: value.NextOpeningAt,
	}
	if value.HandlerAvatar != nil {
		avatarURL, err := links.URL(value.HandlerAvatar.StorageBackend, value.HandlerAvatar.StorageKey)
		if err != nil {
			return appservice.WebsiteVisitorReception{}, err
		}
		reception.HandlerAvatarURL = avatarURL
	}
	return reception, nil
}

// websiteVisitorMessageFromAction 转换访客消息，已就绪的附件同时签发预览和下载地址。
func websiteVisitorMessageFromAction(ctx context.Context, linker *visitorAttachmentLinker, value customerchataction.Message) (appservice.WebsiteVisitorMessage, error) {
	var replyTo *appservice.WebsiteVisitorMessageReference
	if value.ReplyTo != nil {
		replyTo = &appservice.WebsiteVisitorMessageReference{
			ID: value.ReplyTo.ID, Deleted: value.ReplyTo.Deleted,
			Author: string(value.ReplyTo.Author), Preview: *messagePreviewText(&value.ReplyTo.Body, value.ReplyTo.SenderIdentityType),
		}
	}
	var attachment *appservice.WebsiteVisitorAttachment
	if value.Attachment != nil {
		attachment = &appservice.WebsiteVisitorAttachment{
			Name: value.Attachment.Name, ContentType: value.Attachment.ContentType, ByteSize: value.Attachment.ByteSize,
			ImageWidth: value.Attachment.ImageWidth, ImageHeight: value.Attachment.ImageHeight,
			TransferStatus: string(value.Attachment.TransferStatus),
		}
		// 内容尚未就绪的附件不返回地址。
		if value.Attachment.TransferStatus == domain.MessageAttachmentTransferReady {
			links, err := linker.links(ctx, value.Attachment.StorageBackend, value.Attachment.StorageKey, value.Attachment.Name, value.Attachment.ContentType)
			if err != nil {
				return appservice.WebsiteVisitorMessage{}, err
			}
			attachment.PreviewURL, attachment.DownloadURL = links.PreviewURL, links.DownloadURL
		}
	}
	var event *appservice.WebsiteVisitorEvent
	if value.Event != nil {
		event = &appservice.WebsiteVisitorEvent{
			Type: string(value.Event.Type), ServiceSessionID: value.Event.ServiceSessionID, MemberName: value.Event.MemberName, Email: value.Event.Email, ToolCallID: value.Event.ToolCallID,
		}
	}
	senderAvatarURL := ""
	if value.SenderAvatar != nil {
		avatarURL, err := linker.avatarURL(ctx, *value.SenderAvatar)
		if err != nil {
			return appservice.WebsiteVisitorMessage{}, err
		}
		senderAvatarURL = avatarURL
	}
	return appservice.WebsiteVisitorMessage{
		ClientMessageID: value.ClientMessageID, SenderIdentityID: value.SenderIdentityID, SenderName: value.SenderDisplayName, SenderAvatarURL: senderAvatarURL,
		ReplyTo:    replyTo,
		Attachment: attachment,
		Event:      event,
		ID:         value.ID, Author: string(value.Author), Body: value.Body, Preview: *messagePreviewText(&value.Body, value.SenderIdentityType), SenderIdentityType: value.SenderIdentityType,
		MessageSeq: strconv.FormatInt(value.MessageSeq, 10), OriginatedAt: value.OriginatedAt, CreatedAt: value.CreatedAt,
	}, nil
}

// websiteVisitorRatingFromAction 转换访客评价状态。
func websiteVisitorRatingFromAction(value customerchataction.VisitorRating) appservice.WebsiteVisitorRating {
	return appservice.WebsiteVisitorRating{Rateable: value.Rateable, Resolved: value.Resolved, Comment: value.Comment}
}
