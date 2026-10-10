package domain

// PersonalAgentPresence 定义仅服务负责人本人的 AI 员工当前能否处理新请求与操作电脑。
type PersonalAgentPresence string

const (
	// PersonalAgentPresenceOnline 表示使用的电脑在线且 AI 员工未暂停。
	PersonalAgentPresenceOnline PersonalAgentPresence = "online"
	// PersonalAgentPresenceOffline 表示使用的电脑不在线，AI 员工照常回复，电脑上线前无法操作电脑。
	PersonalAgentPresenceOffline PersonalAgentPresence = "offline"
	// PersonalAgentPresencePaused 表示负责人暂停了 AI 员工，不接收新请求。
	PersonalAgentPresencePaused PersonalAgentPresence = "paused"
	// PersonalAgentPresenceUnbound 表示使用的电脑已撤销，负责人换到新电脑后恢复。
	PersonalAgentPresenceUnbound PersonalAgentPresence = "unbound"
	// PersonalAgentPresenceInactive 表示 AI 员工已停用。
	PersonalAgentPresenceInactive PersonalAgentPresence = "inactive"
)

// ResolvePersonalAgentPresence 按账号状态、暂停、电脑撤销状态与电脑是否在线计算仅服务负责人本人的 AI 员工在线状态。
func ResolvePersonalAgentPresence(status IdentityStatus, paused, computerRevoked, computerOnline bool) PersonalAgentPresence {
	switch {
	case status != IdentityStatusActive:
		return PersonalAgentPresenceInactive
	case computerRevoked:
		return PersonalAgentPresenceUnbound
	case paused:
		return PersonalAgentPresencePaused
	case computerOnline:
		return PersonalAgentPresenceOnline
	default:
		return PersonalAgentPresenceOffline
	}
}
