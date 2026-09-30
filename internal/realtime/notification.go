//go:build server

// Package realtime 在写事务内登记受众通知，并在事务提交成功后发布到 NATS。
package realtime

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// AudienceKind 定义通知受众种类。
type AudienceKind string

const (
	AudienceUser             AudienceKind = "user"
	AudienceCustomerInbox    AudienceKind = "customer_inbox"
	AudienceVisitorDirectory AudienceKind = "visitor_directory"
	AudienceWebsiteChannel   AudienceKind = "website_channel"
	AudienceCustomerIdentity AudienceKind = "customer_identity"
	AudienceWebsiteVisitors  AudienceKind = "website_visitors"
)

// Kind 定义通知种类。
type Kind string

const (
	KindConversationChanged      Kind = "conversation_changed"
	KindConversationRemoved      Kind = "conversation_removed"
	KindConversationStateChanged Kind = "conversation_state_changed"
	KindConversationTyping       Kind = "conversation_typing"
	KindVisitorTyping            Kind = "visitor_typing"
	KindIdentityProfileChanged   Kind = "identity_profile_changed"
	KindPinOrderChanged          Kind = "pin_order_changed"
	KindSessionLoggedOut         Kind = "session_logged_out"
	KindUserDisabled             Kind = "user_disabled"
	KindChannelDisabled          Kind = "channel_disabled"
	KindCustomerIdentityRevoked  Kind = "customer_identity_revoked"
	KindServiceAttention         Kind = "service_attention"
	KindDeviceWorkAdvanced       Kind = "device_work_advanced"
	KindReceptionChanged         Kind = "reception_changed"
	KindKnowledgeGapsChanged     Kind = "knowledge_gaps_changed"
	KindServiceReportsChanged    Kind = "service_reports_changed"
	KindAssistantMemoryChanged   Kind = "assistant_memory_changed"
)

// Notification 表示发往单个受众的变更通知、输入状态、客服提醒或撤销控制，载荷含通知种类、会话 ID、会话类型、版本、会话变化类别、登录会话 ID、输入状态、客服提醒原因、设备 ID 与助理 ID，零值字段省略。
type Notification struct {
	OrganizationID   string
	AudienceKind     AudienceKind
	AudienceID       string
	Kind             Kind
	ConversationID   string
	ConversationType domain.ConversationType
	Version          int64
	Changes          domain.ConversationChanges
	TokenSessionID   string
	SenderSubjectID  string
	Active           bool
	ServiceSessionID string
	AttentionReason  domain.ServiceAttentionReason
	DeviceID         string
	AssistantID      string
}

// UserConversationChanged 构造发往用户受众的会话变更通知，携带会话类型与变化类别。
func UserConversationChanged(organizationID, userID, conversationID string, conversationType domain.ConversationType, version int64, changes domain.ConversationChanges) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationChanged, ConversationID: conversationID, ConversationType: conversationType, Version: version, Changes: changes}
}

// ServiceInboxConversationChanged 构造发往企业客服共享受众的客户会话或 Copilot 线程变更通知，携带会话类型与变化类别。
func ServiceInboxConversationChanged(organizationID, conversationID string, conversationType domain.ConversationType, version int64, changes domain.ConversationChanges) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceCustomerInbox, AudienceID: organizationID, Kind: KindConversationChanged, ConversationID: conversationID, ConversationType: conversationType, Version: version, Changes: changes}
}

// VisitorDirectoryConversationChanged 构造发往网站渠道身份受众的客户线程变更通知，受众 ID 为渠道身份记录 ID。
func VisitorDirectoryConversationChanged(organizationID, channelIdentityID, conversationID string, version int64) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceVisitorDirectory, AudienceID: channelIdentityID, Kind: KindConversationChanged, ConversationID: conversationID, Version: version}
}

// UserConversationRemoved 构造发往失去会话阅读资格用户的会话失权通知，载荷不含版本。
func UserConversationRemoved(organizationID, userID, conversationID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationRemoved, ConversationID: conversationID}
}

// UserConversationStateChanged 构造发往本人受众的个人会话状态通知。
func UserConversationStateChanged(organizationID, userID, conversationID string, version int64) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationStateChanged, ConversationID: conversationID, Version: version}
}

// UserConversationTyping 构造发往用户受众的会话输入状态，发送者为会话参与主体编号。
func UserConversationTyping(organizationID, userID, conversationID, senderSubjectID string, active bool) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationTyping, ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active}
}

// ServiceInboxConversationTyping 构造发往企业客服共享受众的服务会话输入状态，发送者为访客或负责 AI 员工的聊天主体编号。
func ServiceInboxConversationTyping(organizationID, conversationID, senderSubjectID string, active bool) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceCustomerInbox, AudienceID: organizationID, Kind: KindConversationTyping, ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active}
}

// VisitorDirectoryTyping 构造发往网站渠道身份受众的访客可见输入状态，受众 ID 为渠道身份记录 ID。
func VisitorDirectoryTyping(organizationID, channelIdentityID, conversationID string, active bool) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceVisitorDirectory, AudienceID: channelIdentityID, Kind: KindVisitorTyping, ConversationID: conversationID, Active: active}
}

// UserIdentityProfileChanged 构造发往本人受众的身份资料通知。
func UserIdentityProfileChanged(organizationID, userID string, version int64) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindIdentityProfileChanged, Version: version}
}

// ServiceInboxKnowledgeGapsChanged 构造发往企业客服共享受众的待补知识变化通知，成员据此重新读取待补知识清单与本人负责的待处理条数。
func ServiceInboxKnowledgeGapsChanged(organizationID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceCustomerInbox, AudienceID: organizationID, Kind: KindKnowledgeGapsChanged}
}

// ServiceInboxReportsChanged 构造发往企业客服共享受众的客服报表变化通知，成员据此重新读取 AI 表现、团队表现报表与问题会话。
func ServiceInboxReportsChanged(organizationID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceCustomerInbox, AudienceID: organizationID, Kind: KindServiceReportsChanged}
}

// UserPinOrderChanged 构造发往本人受众的个人置顶顺序通知，载荷不含会话。
func UserPinOrderChanged(organizationID, userID string, version int64) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindPinOrderChanged, Version: version}
}

// UserAssistantMemoryChanged 构造发往助理主人受众的记忆变更通知，主人据此重新读取该助理的记忆。
func UserAssistantMemoryChanged(organizationID, userID, assistantID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindAssistantMemoryChanged, AssistantID: assistantID}
}

// UserDeviceWorkAdvanced 构造发往设备主人受众的设备工作水位通知，版本为设备最新工作水位，Gateway 只转发给该设备的事件流。
func UserDeviceWorkAdvanced(organizationID, userID, deviceID string, workSeq int64) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindDeviceWorkAdvanced, DeviceID: deviceID, Version: workSeq}
}

// WebsiteChannelDisabled 构造网站渠道停用撤销控制，Gateway 据此结束该渠道全部访客事件流；受众 ID 为渠道 ID。
func WebsiteChannelDisabled(organizationID, channelID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceWebsiteChannel, AudienceID: channelID, Kind: KindChannelDisabled}
}

// WebsiteReceptionChanged 构造发往企业全部网站访客的接待状态变化通知，访客据此重新读取接待状态；受众 ID 为企业 ID。
func WebsiteReceptionChanged(organizationID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceWebsiteVisitors, AudienceID: organizationID, Kind: KindReceptionChanged}
}

// CustomerIdentityRevoked 构造客户身份密钥重新生成的撤销控制，Gateway 据此结束该企业全部以签名身份建立的访客事件流；受众 ID 为企业 ID。
func CustomerIdentityRevoked(organizationID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceCustomerIdentity, AudienceID: organizationID, Kind: KindCustomerIdentityRevoked}
}

// UserSessionLoggedOut 构造登出撤销控制，Gateway 据此关闭该登录会话的连接。
func UserSessionLoggedOut(organizationID, userID, tokenSessionID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindSessionLoggedOut, TokenSessionID: tokenSessionID}
}

// UserDisabled 构造账号停用撤销控制，Gateway 据此关闭该用户的全部连接。
func UserDisabled(organizationID, userID string) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindUserDisabled}
}

// UserServiceAttention 构造发往负责成员本人受众的服务周期提醒。
func UserServiceAttention(organizationID, userID, conversationID, serviceSessionID string, reason domain.ServiceAttentionReason) Notification {
	return Notification{OrganizationID: organizationID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindServiceAttention, ConversationID: conversationID, ServiceSessionID: serviceSessionID, AttentionReason: reason}
}

type batchKey struct{}

// mergeKey 标识可合并的通知：同一受众、同一种类、同一会话、同一登录会话、同一客服处理周期与提醒原因、同一设备。
type mergeKey struct {
	organizationID   string
	audienceKind     AudienceKind
	audienceID       string
	kind             Kind
	conversationID   string
	tokenSessionID   string
	serviceSessionID string
	attentionReason  domain.ServiceAttentionReason
	deviceID         string
	assistantID      string
}

// batch 按登记顺序保存一次事务内合并后的通知。
type batch struct {
	order []mergeKey
	items map[mergeKey]Notification
}

// RunInTx 在 *bun.DB 或 bun.Conn 上执行写事务，事务内通过 Notify 登记的通知在提交成功后交给发布器，回滚则丢弃。
func RunInTx(ctx context.Context, db bun.IDB, fn func(context.Context, bun.Tx) error) error {
	pending := &batch{items: map[mergeKey]Notification{}}
	if err := db.RunInTx(context.WithValue(ctx, batchKey{}, pending), nil, fn); err != nil {
		return err
	}
	if publisher := active.Load(); publisher != nil && len(pending.order) > 0 {
		notifications := make([]Notification, 0, len(pending.order))
		for _, key := range pending.order {
			notifications = append(notifications, pending.items[key])
		}
		publisher.enqueue(notifications)
	}
	return nil
}

// Notify 在当前 RunInTx 事务内登记通知，同一受众、种类和会话合并为最高版本并合并全部变化类别；调用方必须处于 RunInTx 内。
func Notify(ctx context.Context, notification Notification) {
	pending, ok := ctx.Value(batchKey{}).(*batch)
	if !ok {
		panic("realtime: Notify called outside realtime.RunInTx")
	}
	key := mergeKey{notification.OrganizationID, notification.AudienceKind, notification.AudienceID, notification.Kind, notification.ConversationID, notification.TokenSessionID, notification.ServiceSessionID, notification.AttentionReason, notification.DeviceID, notification.AssistantID}
	current, exists := pending.items[key]
	if !exists {
		pending.order = append(pending.order, key)
		pending.items[key] = notification
		return
	}
	changes := current.Changes | notification.Changes
	if notification.Version > current.Version {
		current = notification
	}
	current.Changes = changes
	pending.items[key] = current
}
