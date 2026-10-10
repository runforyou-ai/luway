//go:build server

package realtime

// VisitorChannel 返回渠道身份的精确私有通知频道。
func VisitorChannel(workspaceID, identityID string) string {
	return "w." + workspaceID + ".v." + identityID
}

// ComputerChannel 返回电脑的精确私有工作通知频道。
func ComputerChannel(workspaceID, computerID string) string {
	return "w." + workspaceID + ".c." + computerID
}

// Channels 定义成员本人和共享收件箱的精确频道名称。
type Channels struct {
	Channel, InboxChannel, TypingChannel, InboxTypingChannel string
}

// MemberChannels 返回成员本人与所在工作区共享收件箱的精确频道。
func MemberChannels(workspaceID, memberID string) Channels {
	channel, inbox := "w."+workspaceID+".u."+memberID, "w."+workspaceID+".inbox"
	return Channels{Channel: channel, InboxChannel: inbox, TypingChannel: channel + ".typing", InboxTypingChannel: inbox + ".typing"}
}
