package domain

// MessageType 定义可持久化的会话消息类型。
type MessageType string

const (
	MessageTypeText           MessageType = "text"
	MessageTypeSystem         MessageType = "system"
	MessageTypeAgentError     MessageType = "agent_error"
	MessageTypeAgentCancelled MessageType = "agent_cancelled"
	MessageTypeAttachment     MessageType = "attachment"
	// MessageTypeUnsupported 是发起人经外部平台发送、内容类型暂不支持的消息，正文为空。
	MessageTypeUnsupported MessageType = "unsupported"
)

// UnreadMessageTypes 是计入未读与提醒的消息类型；系统消息另外只有发给发起人的服务进度计入。
var UnreadMessageTypes = []MessageType{MessageTypeText, MessageTypeAttachment, MessageTypeUnsupported, MessageTypeAgentError}

// CountsTowardUnread 判断指定类型与可见范围的消息是否计入未读与提醒。
func CountsTowardUnread(messageType MessageType, visibility MessageVisibility) bool {
	switch messageType {
	case MessageTypeText, MessageTypeAttachment, MessageTypeUnsupported, MessageTypeAgentError:
		return true
	case MessageTypeSystem:
		return visibility == MessageVisibilityRequester
	default:
		return false
	}
}
