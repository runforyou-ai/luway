package domain

import "time"

// DeviceOnlineWindow 是电脑最近一次在线后仍视为在线的时长，覆盖设备的工作轮询间隔与在线时间刷新粒度。
const DeviceOnlineWindow = 90 * time.Second

// PersonalAgentPresence 定义仅服务负责人本人的 AI 员工当前能否处理新请求。
type PersonalAgentPresence string

const (
	// PersonalAgentPresenceOnline 表示绑定电脑在线且 AI 员工未暂停。
	PersonalAgentPresenceOnline PersonalAgentPresence = "online"
	// PersonalAgentPresenceOffline 表示绑定电脑不在线，新请求在电脑上线后处理。
	PersonalAgentPresenceOffline PersonalAgentPresence = "offline"
	// PersonalAgentPresencePaused 表示负责人暂停了 AI 员工，不接收新请求。
	PersonalAgentPresencePaused PersonalAgentPresence = "paused"
	// PersonalAgentPresenceUnbound 表示绑定电脑已撤销，负责人在新电脑上换绑后恢复。
	PersonalAgentPresenceUnbound PersonalAgentPresence = "unbound"
	// PersonalAgentPresenceInactive 表示 AI 员工已停用。
	PersonalAgentPresenceInactive PersonalAgentPresence = "inactive"
)

// ResolvePersonalAgentPresence 按账号状态、暂停、绑定电脑撤销状态与最近在线时间计算仅服务负责人本人的 AI 员工在线状态。
func ResolvePersonalAgentPresence(status IdentityStatus, paused, deviceRevoked bool, deviceLastSeenAt *time.Time, now time.Time) PersonalAgentPresence {
	switch {
	case status != IdentityStatusActive:
		return PersonalAgentPresenceInactive
	case deviceRevoked:
		return PersonalAgentPresenceUnbound
	case paused:
		return PersonalAgentPresencePaused
	case deviceLastSeenAt != nil && now.Sub(*deviceLastSeenAt) < DeviceOnlineWindow:
		return PersonalAgentPresenceOnline
	default:
		return PersonalAgentPresenceOffline
	}
}
