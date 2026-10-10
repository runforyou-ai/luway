package appservice

// RealtimeConnection 是账号在部署入口建立成员实时连接所需的订阅配置。
type RealtimeConnection struct {
	UserID    string           `json:"userId"`
	SessionID string           `json:"sessionId"`
	Prefix    string           `json:"prefix"`
	Path      string           `json:"path"`
	Members   []RealtimeMember `json:"members"`
}

// RealtimeMember 是一个有效工作区成员可订阅的私有频道。
type RealtimeMember struct {
	WorkspaceID        string `json:"workspaceId"`
	UserID             string `json:"userId"`
	Channel            string `json:"channel"`
	InboxChannel       string `json:"inboxChannel"`
	TypingChannel      string `json:"typingChannel"`
	InboxTypingChannel string `json:"inboxTypingChannel"`
}
