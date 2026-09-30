package domain

import "time"

// DeviceOnlineWindow 是电脑最近一次在线后仍视为在线的时长，覆盖设备的工作轮询间隔与在线时间刷新粒度。
const DeviceOnlineWindow = 90 * time.Second

// AssistantPresence 定义助理当前能否处理新请求。
type AssistantPresence string

const (
	// AssistantPresenceOnline 表示绑定电脑在线且助理未暂停。
	AssistantPresenceOnline AssistantPresence = "online"
	// AssistantPresenceOffline 表示绑定电脑不在线，新请求在电脑上线后处理。
	AssistantPresenceOffline AssistantPresence = "offline"
	// AssistantPresencePaused 表示主人暂停了助理，不接收新请求。
	AssistantPresencePaused AssistantPresence = "paused"
	// AssistantPresenceUnbound 表示绑定电脑已撤销，主人在新电脑上换绑后恢复。
	AssistantPresenceUnbound AssistantPresence = "unbound"
	// AssistantPresenceInactive 表示助理已停用。
	AssistantPresenceInactive AssistantPresence = "inactive"
)

// ResolveAssistantPresence 按账号状态、暂停、绑定电脑撤销状态与最近在线时间计算助理在线状态。
func ResolveAssistantPresence(status IdentityStatus, paused, deviceRevoked bool, deviceLastSeenAt *time.Time, now time.Time) AssistantPresence {
	switch {
	case status != IdentityStatusActive:
		return AssistantPresenceInactive
	case deviceRevoked:
		return AssistantPresenceUnbound
	case paused:
		return AssistantPresencePaused
	case deviceLastSeenAt != nil && now.Sub(*deviceLastSeenAt) < DeviceOnlineWindow:
		return AssistantPresenceOnline
	default:
		return AssistantPresenceOffline
	}
}
