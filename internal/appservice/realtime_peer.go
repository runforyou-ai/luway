package appservice

// RealtimePeerConnection 定义访客与电脑实时连接的短期凭据及精确私有频道。
type RealtimePeerConnection struct {
	Token            string `json:"token"`
	UserID           string `json:"userId"`
	Channel          string `json:"channel"`
	TypingChannel    string `json:"typingChannel,omitempty"`
	ReceptionChannel string `json:"receptionChannel,omitempty"`
	Prefix           string `json:"prefix"`
	Path             string `json:"path"`
}
