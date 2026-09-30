package domain

// MessageType 定义可持久化的会话消息类型。
type MessageType string

const (
	MessageTypeText           MessageType = "text"
	MessageTypeSystem         MessageType = "system"
	MessageTypeAgentError     MessageType = "agent_error"
	MessageTypeAgentCancelled MessageType = "agent_cancelled"
	MessageTypeAttachment     MessageType = "attachment"
)
