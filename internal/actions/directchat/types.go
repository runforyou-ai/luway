//go:build server

// Package directchat 处理成员单聊、AI 员工聊天与客服 Copilot 线程的消息收发。
package directchat

import (
	"time"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// FirstDirectTextMessageInput 定义成员向目标身份发送的首条单聊消息。
type FirstDirectTextMessageInput struct {
	TargetIdentityID string
	ClientMessageID  string
	Body             string
}

// FirstDirectTextMessageResult 定义首条单聊消息及其确定的长期会话。
type FirstDirectTextMessageResult struct {
	Conversation DirectConversationSummary
	Message      conversationaction.ConversationMessage
}

// DirectConversationSummary 定义成员内部单聊摘要。
type DirectConversationSummary struct {
	LastActivityAt            *time.Time
	ID                        string
	PeerIdentityID            string
	PeerType                  domain.OrganizationIdentityType
	PeerName                  string
	PeerAvatarFileID          *string
	Preview                   *string
	PreviewSenderIdentityType *domain.OrganizationIdentityType
	LastMessageAt             *time.Time
}

// InternalTextMessageInput 定义成员发送的内部单聊文本消息。
type InternalTextMessageInput struct {
	ConversationID   string
	ClientMessageID  string
	Body             string
	ReplyToMessageID string
}

// AttachmentMessageInput 定义已上传附件的发送意图，AgentIdentityID 非空表示按 ConversationID 草稿编号首发 AI 聊天，同时指定 ServedConversationID 表示首发该服务会话的 Copilot 线程。
type AttachmentMessageInput struct {
	ConversationID       string
	TargetIdentityID     string
	AgentIdentityID      string
	ServedConversationID string
	ClientMessageID      string
	FileID               string
	Body                 string
	ImageWidth           int
	ImageHeight          int
}

// AttachmentMessageResult 返回附件消息，首发时返回新建单聊或 AI 聊天摘要。
type AttachmentMessageResult struct {
	ConversationID    string
	Conversation      *DirectConversationSummary
	AgentConversation *inboxaction.ConversationSummary
	Message           conversationaction.ConversationMessage
}
