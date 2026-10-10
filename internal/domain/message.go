package domain

// MessageAuthor 定义消息发送方。
type MessageAuthor string

const (
	MessageAuthorVisitor MessageAuthor = "visitor"
	MessageAuthorAgent   MessageAuthor = "agent"
	MessageAuthorSystem  MessageAuthor = "system"
)

// MessageVisibility 定义消息在服务会话中的可见范围：shared 会话各方可见，internal 仅处理方可见，requester 仅企业成员发起人可见。
type MessageVisibility string

const (
	MessageVisibilityShared    MessageVisibility = "shared"
	MessageVisibilityInternal  MessageVisibility = "internal"
	MessageVisibilityRequester MessageVisibility = "requester"
)
