//go:build server

package members

import (
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

// NotificationFrame 把持久通知或临时输入状态转换为跨端事件契约。
func NotificationFrame(n realtime.Notification) protocol.Frame {
	switch n.Kind {
	case realtime.KindConversationChanged:
		return protocol.ConversationChanged{ConversationID: n.ConversationID, ConversationType: n.ConversationType, Version: n.Version, Changes: n.Changes}
	case realtime.KindConversationRemoved:
		return protocol.ConversationRemoved{ConversationID: n.ConversationID}
	case realtime.KindConversationStateChanged:
		return protocol.ConversationStateChanged{ConversationID: n.ConversationID, Version: n.Version}
	case realtime.KindConversationTyping:
		return protocol.ConversationTyping{ConversationID: n.ConversationID, SenderSubjectID: n.SenderSubjectID, Active: n.Active}
	case realtime.KindIdentityProfileChanged:
		return protocol.IdentityProfileChanged{Version: n.Version}
	case realtime.KindPinOrderChanged:
		return protocol.PinOrderChanged{Version: n.Version}
	case realtime.KindKnowledgeGapsChanged:
		return protocol.KnowledgeGapsChanged{}
	case realtime.KindServiceReportsChanged:
		return protocol.ServiceReportsChanged{}
	case realtime.KindChannelChanged:
		return protocol.ChannelChanged{ChannelID: n.ChannelID}
	case realtime.KindAgentMemoryChanged:
		return protocol.AgentMemoryChanged{AgentID: n.AgentID}
	case realtime.KindToolDecisionsChanged:
		return protocol.ToolDecisionsChanged{}
	case realtime.KindUserNotification:
		return protocol.UserNotification{WorkspaceID: n.WorkspaceID, UserID: n.AudienceID, ID: n.NotificationID, ConversationID: n.ConversationID, View: n.View, Title: n.Title, Body: n.Body}
	}
	return nil
}
