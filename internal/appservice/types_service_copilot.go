package appservice

import "time"

// ServiceCopilotThread 定义服务会话中 Copilot 线程的摘要。
type ServiceCopilotThread struct {
	ID                  string    `json:"id"`
	Title               string    `json:"title"`
	AgentIdentityID     string    `json:"agentIdentityId"`
	AgentName           string    `json:"agentName"`
	AgentAvatarURL      string    `json:"agentAvatarUrl"`
	AgentActive         bool      `json:"agentActive"`
	CreatedByIdentityID string    `json:"createdByIdentityId"`
	CreatedByName       string    `json:"createdByName"`
	CreatedAt           time.Time `json:"createdAt"`
	LastActivityAt      time.Time `json:"lastActivityAt"`
}

// ServiceCopilotThreadList 定义服务会话按最近活动倒序排列的 Copilot 线程。
type ServiceCopilotThreadList struct {
	Threads []ServiceCopilotThread `json:"threads"`
}

// FirstServiceCopilotMessageInput 定义新线程的稳定编号、回答的 AI 员工和首条提问。
type FirstServiceCopilotMessageInput struct {
	ThreadID        string `json:"threadId"`
	AgentIdentityID string `json:"agentIdentityId"`
	ClientMessageID string `json:"clientMessageId"`
	Body            string `json:"body"`
}

// FirstServiceCopilotMessageResult 定义首条提问确认的线程和消息。
type FirstServiceCopilotMessageResult struct {
	Thread  ServiceCopilotThread `json:"thread"`
	Message ConversationMessage  `json:"message"`
}

// ServiceCopilotTextMessageInput 定义发给 Copilot 线程的成员提问。
type ServiceCopilotTextMessageInput struct {
	ClientMessageID  string `json:"clientMessageId"`
	Body             string `json:"body"`
	ReplyToMessageID string `json:"replyToMessageId"`
}
