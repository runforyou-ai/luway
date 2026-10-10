package domain

// ConversationType 定义会话类型。
type ConversationType string

const (
	ConversationTypeAgent   ConversationType = "agent"
	ConversationTypeDirect  ConversationType = "direct"
	ConversationTypeGroup   ConversationType = "group"
	ConversationTypeChannel ConversationType = "channel"
	ConversationTypeCopilot ConversationType = "copilot"
)

// ConversationStatus 定义会话生命周期状态。
type ConversationStatus string

const (
	ConversationStatusActive   ConversationStatus = "active"
	ConversationStatusArchived ConversationStatus = "archived"
)
