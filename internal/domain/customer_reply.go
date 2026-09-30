package domain

// ServiceReplyMode 定义 AI 写回复的生成方式。
type ServiceReplyMode string

const (
	// ServiceReplyModeReply 根据会话上下文撰写回复。
	ServiceReplyModeReply ServiceReplyMode = "reply"
	// ServiceReplyModeRewrite 保留客服草稿原意并改写表达。
	ServiceReplyModeRewrite ServiceReplyMode = "rewrite"
)

// ServiceReplyTone 定义 AI 写回复的语气。
type ServiceReplyTone string

const (
	ServiceReplyToneKeep         ServiceReplyTone = "keep"
	ServiceReplyToneProfessional ServiceReplyTone = "professional"
	ServiceReplyToneFriendly     ServiceReplyTone = "friendly"
	ServiceReplyToneConcise      ServiceReplyTone = "concise"
)
