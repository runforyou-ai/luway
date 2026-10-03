//go:build server

// Package customerchat 处理外部渠道与网站访客进入会话的消息收发、访客会话目录、附件、评价和身份校验。
package customerchat

import (
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	ValidationChannelIDInvalid     conversationaction.ValidationCode = "channel_id_invalid"
	ValidationExternalIDInvalid    conversationaction.ValidationCode = "external_id_invalid"
	ValidationRatingCommentTooLong conversationaction.ValidationCode = "rating_comment_too_long"
)

const (
	// ConflictReasonServiceSessionNotRateable 表示渠道未开启评价，或服务周期未关闭、已评价。
	ConflictReasonServiceSessionNotRateable = "service_session_not_rateable"
	// ConflictReasonAttachmentsDisabled 表示网站渠道未开启访客发送附件。
	ConflictReasonAttachmentsDisabled = "attachments_disabled"
)

// VisitorEventType 定义访客可见的客服处理周期事件类型。
type VisitorEventType string

const (
	VisitorEventMemberJoined VisitorEventType = "member_joined"
	VisitorEventSessionEnded VisitorEventType = "session_ended"
	// VisitorEventEmailCollected 表示访客留下了接收回复的邮箱。
	VisitorEventEmailCollected VisitorEventType = "email_collected"
)

// WebsiteCustomerTextMessageInput 定义网站客户文本消息。
type WebsiteCustomerTextMessageInput struct {
	ReplyToMessageID string
	ChannelID        string
	ExternalID       string
	ConversationID   *string
	ClientMessageID  string
	Body             string
	Customer         *SignedCustomer
	VisitorContext   *domain.VisitorContext
}

// SignedCustomer 表示客户签名身份验签通过的企业用户；Profile 是签名身份带入的联系人档案。
type SignedCustomer struct {
	UserID  string
	Name    string
	Email   string
	Profile domain.SignedContactProfile
}

// WebsiteCustomerAttachmentMessageInput 定义网站访客发送的附件消息。
type WebsiteCustomerAttachmentMessageInput struct {
	ReplyToMessageID string
	ChannelID        string
	ExternalID       string
	ConversationID   *string
	ClientMessageID  string
	FileID           string
	Body             string
	ImageWidth       int
	ImageHeight      int
	Customer         *SignedCustomer
	VisitorContext   *domain.VisitorContext
}

// WebsiteVisitorUploadInput 定义网站访客附件上传的渠道归属与文件元数据。
type WebsiteVisitorUploadInput struct {
	ChannelID   string
	ExternalID  string
	FileName    string
	ContentType string
	ByteSize    int64
	Customer    *SignedCustomer
}

// ConversationSummary 定义访客可见会话摘要。
type ConversationSummary struct {
	LastMessageSeq            int64
	ID                        string
	Title                     string
	Preview                   string
	PreviewSenderIdentityType *domain.OrganizationIdentityType
	LastMessageAt             time.Time
	ServiceSessionID          string
	ServiceSessionStatus      domain.ServiceSessionStatus
	// ServiceSessionTeamID 与 ServiceSessionAssigneeID 是当前客服周期所在队列与负责人，Reception 由二者推导。
	ServiceSessionTeamID     *string
	ServiceSessionAssigneeID *string
	Reception                chatstate.Reception
}

// WebsiteConversationDirectory 定义网站访客的客户线程目录、新会话的接待状态，以及接待状态随工作时间可能变化的下一时刻。
type WebsiteConversationDirectory struct {
	NewSessionReception chatstate.Reception
	ReceptionRefreshAt  *time.Time
	Conversations       []ConversationSummary
}

// MessageReference 定义访客可见的一层引用摘要。
type MessageReference struct {
	SenderIdentityType *domain.OrganizationIdentityType
	ID                 string
	Deleted            bool
	Author             domain.MessageAuthor
	Body               string
}

// Message 定义访客可见消息。
type Message struct {
	ClientMessageID    *string
	MessageSeq         int64
	ReplyTo            *MessageReference
	Attachment         *VisitorAttachment
	ID                 string
	Author             domain.MessageAuthor
	SenderIdentityType *domain.OrganizationIdentityType
	// SenderIdentityID、SenderDisplayName 和 SenderAvatar 仅在组织身份发送时有值。
	SenderIdentityID  string
	SenderDisplayName string
	SenderAvatar      *FileLocation
	Body              string
	// Event 仅在 Author 为 system 时有值。
	Event        *VisitorEvent
	OriginatedAt time.Time
	SourceOrder  int64
	CreatedAt    time.Time
}

// VisitorEvent 定义访客时间线中的客服处理周期事件，成员加入时带成员名称，留下邮箱时带邮箱。
type VisitorEvent struct {
	Type             VisitorEventType
	ServiceSessionID string
	MemberName       string
	Email            string
}

// VisitorRating 定义访客对客服处理周期的评价状态；Rateable 为真时尚未评价。
type VisitorRating struct {
	Rateable bool
	Resolved *bool
	Comment  string
}

// VisitorSessionRating 定义客服处理周期的评价状态及其挂载的最近一次结束事件。
type VisitorSessionRating struct {
	ServiceSessionID string
	EndMessageID     string
	VisitorRating
}

// FileLocation 定义文件的存储位置。
type FileLocation struct {
	StorageBackend domain.FileStorageBackend
	StorageKey     string
}

// ReceiveWebsiteCustomerMessageResult 定义网站消息写入结果。
type ReceiveWebsiteCustomerMessageResult struct {
	OrganizationID          string
	Conversation            ConversationSummary
	CreatedConversation     bool
	OpenedNewServiceSession bool
	Message                 Message
}

// MessageHistoryInput 定义消息历史查询方向。
type MessageHistoryInput struct {
	ChannelID      string
	ExternalID     string
	ConversationID string
	Before         *conversationaction.MessageCursorPoint
	After          *conversationaction.MessageCursorPoint
}

// MessageHistory 定义消息历史和下一页边界。
type MessageHistory struct {
	OrganizationID string
	Messages       []Message
	// SessionRatings 覆盖线程内全部已关闭或已评价的周期，与消息分页无关。
	SessionRatings []VisitorSessionRating
	Before         *conversationaction.MessageCursorPoint
	After          *conversationaction.MessageCursorPoint
}

// VisitorAttachment 定义访客可见的附件元数据与内容存储位置。
type VisitorAttachment struct {
	conversationaction.MessageAttachment
	StorageBackend domain.FileStorageBackend `bun:"storage_backend"`
	StorageKey     string                    `bun:"storage_key"`
}
