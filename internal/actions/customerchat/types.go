//go:build server

// Package customerchat 处理外部渠道与网站访客进入会话的消息收发、访客会话目录、附件、评价和身份校验。
package customerchat

import (
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
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
	// VisitorEventMemberJoined 表示成员加入接待。
	VisitorEventMemberJoined VisitorEventType = "member_joined"
	// VisitorEventSessionEnded 表示客服处理周期结束。
	VisitorEventSessionEnded VisitorEventType = "session_ended"
	// VisitorEventEmailCollected 表示访客留下了接收回复的邮箱。
	VisitorEventEmailCollected VisitorEventType = "email_collected"
	// VisitorEventToolConfirmation 表示 AI 员工提交了等待访客确认的操作，确认卡片的内容与状态取自消息历史的操作列表。
	VisitorEventToolConfirmation VisitorEventType = "tool_confirmation"
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
	// ChannelID 与 ExternalID 是渠道身份断言绑定的渠道编号与外部编号，签名身份未提供时为空。
	ChannelID  string
	ExternalID string
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
	PreviewSenderIdentityType *domain.WorkspaceIdentityType
	LastMessageAt             time.Time
	ServiceSessionID          string
	ServiceSessionStatus      domain.ServiceSessionStatus
	// ServiceSessionTeamID 与 ServiceSessionAssigneeID 是当前客服周期所在队列与负责人，Reception 由二者推导。
	ServiceSessionTeamID     *string
	ServiceSessionAssigneeID *string
	Reception                serviceroute.Reception
}

// WebsiteConversationDirectory 定义网站访客的客户线程目录、新会话的接待状态，以及接待状态随工作时间可能变化的下一时刻。
type WebsiteConversationDirectory struct {
	NewSessionReception serviceroute.Reception
	ReceptionRefreshAt  *time.Time
	Conversations       []ConversationSummary
}

// MessageReference 定义访客可见的一层引用摘要。
type MessageReference struct {
	SenderIdentityType *domain.WorkspaceIdentityType
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
	Attachment         *channelinbound.Attachment
	ID                 string
	Author             domain.MessageAuthor
	SenderIdentityType *domain.WorkspaceIdentityType
	// SenderIdentityID、SenderDisplayName 和 SenderAvatar 仅在工作区身份发送时有值。
	SenderIdentityID  string
	SenderDisplayName string
	SenderAvatar      *FileLocation
	Body              string
	// Event 仅在 Author 为 system 时有值。
	Event        *VisitorEvent
	OriginatedAt time.Time
	CreatedAt    time.Time
}

// VisitorEvent 定义访客时间线中的客服处理周期事件，成员加入时带成员名称，留下邮箱时带邮箱。
type VisitorEvent struct {
	Type             VisitorEventType
	ServiceSessionID string
	MemberName       string
	Email            string
	ToolCallID       string
}

// VisitorToolCall 定义 AI 员工提交给访客确认的操作：工具名称、模型给出的参数与参数标题、当前状态与截止时间；Decidable 为真时访客可以确认或拒绝。
type VisitorToolCall struct {
	ID             string                     `bun:"id"`
	Name           string                     `bun:"name"`
	Arguments      string                     `bun:"arguments"`
	ArgumentTitles map[string]string          `bun:"argument_titles,type:jsonb"`
	Status         domain.AgentToolCallStatus `bun:"status"`
	ExpiresAt      *time.Time                 `bun:"expires_at"`
	Decidable      bool                       `bun:"decidable"`
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
	WorkspaceID             string
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
	WorkspaceID string
	Messages    []Message
	// SessionRatings 覆盖线程内全部已关闭或已评价的周期，ToolCalls 覆盖线程内全部提交给访客确认的操作，均与消息分页无关。
	SessionRatings []VisitorSessionRating
	ToolCalls      []VisitorToolCall
	Before         *conversationaction.MessageCursorPoint
	After          *conversationaction.MessageCursorPoint
}
