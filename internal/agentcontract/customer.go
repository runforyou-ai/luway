package agentcontract

import "time"

// CustomerProfile 是企业记录的客户档案：阶段、标签、以字段名称为键的自定义资料与内部备注。
type CustomerProfile struct {
	Stage  string            `json:"stage"`
	Tags   []string          `json:"tags,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
	Notes  string            `json:"notes,omitempty"`
}

// CustomerHistorySummary 是客户过往一次咨询的小结。
type CustomerHistorySummary struct {
	ClosedAt time.Time `json:"closedAt"`
	Summary  string    `json:"summary"`
	Category string    `json:"category,omitempty"`
	Resolved *bool     `json:"resolved,omitempty"`
}

// CustomerHistoryResult 按相关度从高到低列出命中的过往咨询，没有命中时 Message 说明结果。
type CustomerHistoryResult struct {
	Sessions []CustomerHistorySession `json:"sessions"`
	Message  string                   `json:"message,omitempty"`
}

// CustomerHistorySession 是命中的一次过往咨询：小结与命中消息前后的对客消息。
type CustomerHistorySession struct {
	CustomerHistorySummary
	Messages []CustomerHistoryMessage `json:"messages"`
}

// CustomerHistoryMessage 是过往咨询中的一条对客消息，Sender 为 customer、agent 或 member，Attachment 是附件消息的文件名。
type CustomerHistoryMessage struct {
	Sender     string    `json:"sender"`
	Body       string    `json:"body"`
	Attachment string    `json:"attachment,omitempty"`
	SentAt     time.Time `json:"sentAt"`
}
