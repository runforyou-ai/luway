//go:build server

// Package realtime 在写事务内登记受众通知并在提交后向成员实时服务或消息总线广播，同时经消息总线在服务端实例之间转发 Agent 运行流。
package realtime

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// AudienceKind 定义通知受众种类。
type AudienceKind string

// 通知受众种类。
const (
	// AudienceAccount 是账号的实时连接控制受众，受众 ID 为账号编号。
	AudienceAccount          AudienceKind = "account"
	AudienceUser             AudienceKind = "user"
	AudienceCustomerInbox    AudienceKind = "customer_inbox"
	AudienceVisitorDirectory AudienceKind = "visitor_directory"
	AudienceWebsiteChannel   AudienceKind = "website_channel"
	// AudienceCustomerIdentity 是工作区内全部以客户身份签名建立的访客连接，受众 ID 为工作区编号。
	AudienceCustomerIdentity AudienceKind = "customer_identity"
	AudienceWebsiteVisitors  AudienceKind = "website_visitors"
	AudienceComputer         AudienceKind = "computer"
	// AudienceAgentRun 是正在执行某次 Agent 运行的服务端实例，受众 ID 为运行编号。
	AudienceAgentRun AudienceKind = "agent_run"
	// AudienceAgentLane 是正在执行某个输入队列运行的服务端实例，受众 ID 为输入队列编号。
	AudienceAgentLane AudienceKind = "agent_lane"
	// AudienceAgentToolCall 是等待某个工具调用结果的服务端实例，受众 ID 为工具调用编号。
	AudienceAgentToolCall AudienceKind = "agent_tool_call"
)

// Kind 定义通知种类。
type Kind string

// 通知种类。
const (
	KindMembershipAdded           Kind = "membership_added"
	KindConversationChanged       Kind = "conversation_changed"
	KindConversationRemoved       Kind = "conversation_removed"
	KindConversationStateChanged  Kind = "conversation_state_changed"
	KindConversationTyping        Kind = "conversation_typing"
	KindVisitorTyping             Kind = "visitor_typing"
	KindIdentityProfileChanged    Kind = "identity_profile_changed"
	KindPinOrderChanged           Kind = "pin_order_changed"
	KindSessionLoggedOut          Kind = "session_logged_out"
	KindUserDisabled              Kind = "user_disabled"
	KindWorkspaceStatusChanged    Kind = "workspace_status_changed"
	KindChannelDisabled           Kind = "channel_disabled"
	KindCustomerIdentityRevoked   Kind = "customer_identity_revoked"
	KindUserNotification          Kind = "user_notification"
	KindComputerWork              Kind = "computer_work"
	KindComputerCredentialRevoked Kind = "computer_credential_revoked"
	KindReceptionChanged          Kind = "reception_changed"
	KindKnowledgeGapsChanged      Kind = "knowledge_gaps_changed"
	KindServiceReportsChanged     Kind = "service_reports_changed"
	KindChannelChanged            Kind = "channel_changed"
	KindAgentMemoryChanged        Kind = "agent_memory_changed"
	KindToolDecisionsChanged      Kind = "tool_decisions_changed"
	KindAgentRunEnded             Kind = "agent_run_ended"
	KindAgentInputAdded           Kind = "agent_input_added"
	KindAgentToolCallSettled      Kind = "agent_tool_call_settled"
)

// Notification 表示发往单个受众的变更通知、输入状态、用户通知或撤销控制，载荷含通知种类、会话 ID、会话类型、版本、会话变化类别、登录会话 ID、输入状态、用户通知内容、AI 员工 ID 与渠道 ID，零值字段省略。
type Notification struct {
	WorkspaceID      string
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
	AgentID          string
	ChannelID        string
	// NotificationID、Title、Body 与 View 是用户通知的编号、标题、正文和点击后打开会话的视图。
	NotificationID string
	Title          string
	Body           string
	View           domain.NotificationView
}

// UserConversationChanged 构造发往用户受众的会话变更通知，携带会话类型与变化类别。
func UserConversationChanged(workspaceID, userID, conversationID string, conversationType domain.ConversationType, version int64, changes domain.ConversationChanges) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationChanged, ConversationID: conversationID, ConversationType: conversationType, Version: version, Changes: changes}
}

// ServiceInboxConversationChanged 构造发往企业客服共享受众的客户会话或 Copilot 线程变更通知，携带会话类型与变化类别。
func ServiceInboxConversationChanged(workspaceID, conversationID string, conversationType domain.ConversationType, version int64, changes domain.ConversationChanges) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerInbox, AudienceID: workspaceID, Kind: KindConversationChanged, ConversationID: conversationID, ConversationType: conversationType, Version: version, Changes: changes}
}

// VisitorDirectoryConversationChanged 构造发往网站渠道身份受众的客户线程变更通知，受众 ID 为渠道身份记录 ID。
func VisitorDirectoryConversationChanged(workspaceID, channelIdentityID, conversationID string, version int64) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceVisitorDirectory, AudienceID: channelIdentityID, Kind: KindConversationChanged, ConversationID: conversationID, Version: version}
}

// UserConversationRemoved 构造发往失去会话阅读资格用户的会话失权通知，载荷不含版本。
func UserConversationRemoved(workspaceID, userID, conversationID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationRemoved, ConversationID: conversationID}
}

// UserConversationStateChanged 构造发往本人受众的个人会话状态通知。
func UserConversationStateChanged(workspaceID, userID, conversationID string, version int64) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationStateChanged, ConversationID: conversationID, Version: version}
}

// UserConversationTyping 构造发往用户受众的会话输入状态，发送者为会话参与主体编号。
func UserConversationTyping(workspaceID, userID, conversationID, senderSubjectID string, active bool) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindConversationTyping, ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active}
}

// ServiceInboxConversationTyping 构造发往企业客服共享受众的服务会话输入状态，发送者为访客或负责 AI 员工的聊天主体编号。
func ServiceInboxConversationTyping(workspaceID, conversationID, senderSubjectID string, active bool) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerInbox, AudienceID: workspaceID, Kind: KindConversationTyping, ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active}
}

// VisitorDirectoryTyping 构造发往网站渠道身份受众的访客可见输入状态，受众 ID 为渠道身份记录 ID。
func VisitorDirectoryTyping(workspaceID, channelIdentityID, conversationID string, active bool) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceVisitorDirectory, AudienceID: channelIdentityID, Kind: KindVisitorTyping, ConversationID: conversationID, Active: active}
}

// UserIdentityProfileChanged 构造发往本人受众的身份资料通知。
func UserIdentityProfileChanged(workspaceID, userID string, version int64) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindIdentityProfileChanged, Version: version}
}

// ServiceInboxKnowledgeGapsChanged 构造发往企业客服共享受众的待补知识变化通知，成员据此重新读取待补知识清单与本人负责的待处理条数。
func ServiceInboxKnowledgeGapsChanged(workspaceID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerInbox, AudienceID: workspaceID, Kind: KindKnowledgeGapsChanged}
}

// ServiceInboxReportsChanged 构造发往企业客服共享受众的客服报表变化通知，成员据此重新读取 AI 表现、团队表现报表与问题会话。
func ServiceInboxReportsChanged(workspaceID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerInbox, AudienceID: workspaceID, Kind: KindServiceReportsChanged}
}

// ServiceInboxChannelChanged 构造发往企业客服共享受众的渠道连接状态变化通知，成员据此重新读取该渠道详情。
func ServiceInboxChannelChanged(workspaceID, channelID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerInbox, AudienceID: workspaceID, Kind: KindChannelChanged, ChannelID: channelID}
}

// UserPinOrderChanged 构造发往本人受众的个人置顶顺序通知，载荷不含会话。
func UserPinOrderChanged(workspaceID, userID string, version int64) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindPinOrderChanged, Version: version}
}

// UserToolDecisionsChanged 构造发往本人受众的待处理操作变更通知，成员据此重新读取待本人确认、审批与核对的操作。
func UserToolDecisionsChanged(workspaceID, userID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindToolDecisionsChanged}
}

// UserAgentMemoryChanged 构造发往个人 AI 员工负责人受众的记忆变更通知，负责人据此重新读取该 AI 员工的记忆。
func UserAgentMemoryChanged(workspaceID, userID, agentID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindAgentMemoryChanged, AgentID: agentID}
}

// ComputerWork 构造发往电脑受众的待执行操作通知，执行器据此领取操作。
func ComputerWork(workspaceID, computerID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceComputer, AudienceID: computerID, Kind: KindComputerWork}
}

// ComputerCredentialRevoked 构造电脑凭据失效控制，电脑撤销或凭据重置时实时服务据此关闭该电脑的实时连接。
func ComputerCredentialRevoked(workspaceID, computerID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceComputer, AudienceID: computerID, Kind: KindComputerCredentialRevoked}
}

// AgentRunEnded 构造运行已结束的控制通知，正在执行该运行的服务端实例据此中断模型调用与进程内工具。
func AgentRunEnded(workspaceID, runID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceAgentRun, AudienceID: runID, Kind: KindAgentRunEnded}
}

// WebsiteChannelDisabled 构造网站渠道停用撤销控制，实时服务据此关闭该渠道全部访客的实时连接；受众 ID 为渠道 ID。
func WebsiteChannelDisabled(workspaceID, channelID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceWebsiteChannel, AudienceID: channelID, Kind: KindChannelDisabled}
}

// WebsiteReceptionChanged 构造发往企业全部网站访客的接待状态变化通知，访客据此重新读取接待状态；受众 ID 为企业 ID。
func WebsiteReceptionChanged(workspaceID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceWebsiteVisitors, AudienceID: workspaceID, Kind: KindReceptionChanged}
}

// CustomerIdentityRevoked 构造客户身份密钥重新生成的撤销控制，实时服务据此关闭该工作区全部以签名身份建立的访客实时连接；受众 ID 为工作区 ID。
func CustomerIdentityRevoked(workspaceID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceCustomerIdentity, AudienceID: workspaceID, Kind: KindCustomerIdentityRevoked}
}

// UserSessionLoggedOut 构造登出撤销控制，实时服务据此关闭该登录会话的成员与运行连接。
func UserSessionLoggedOut(workspaceID, userID, tokenSessionID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindSessionLoggedOut, TokenSessionID: tokenSessionID}
}

// UserDisabled 构造成员停用撤销控制，实时服务据此关闭该成员的全部连接。
func UserDisabled(workspaceID, userID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindUserDisabled}
}

// UserWorkspaceStatusChanged 构造工作区暂停或恢复的撤销控制，实时服务据此关闭该成员的全部连接，客户端复核业务会话后按当前工作区状态订阅。
func UserWorkspaceStatusChanged(workspaceID, userID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindWorkspaceStatusChanged}
}

// UserNotificationFor 构造发往接收成员本人受众的用户通知，标题与正文已按接收人语言生成。
func UserNotificationFor(workspaceID, userID, notificationID, conversationID string, view domain.NotificationView, title, body string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceUser, AudienceID: userID, Kind: KindUserNotification, ConversationID: conversationID, NotificationID: notificationID, View: view, Title: title, Body: body}
}

// batchKey 是上下文中当前事务通知批次的键。
type batchKey struct{}

// mergeKey 标识可合并的通知：同一受众、同一种类、同一会话、同一发送者、同一登录会话、同一用户通知、同一 AI 员工、同一渠道。
type mergeKey struct {
	workspaceID    string
	audienceKind   AudienceKind
	audienceID     string
	kind           Kind
	conversationID string
	senderID       string
	tokenSessionID string
	notificationID string
	agentID        string
	channelID      string
}

// notificationKey 返回通知的合并键。
func notificationKey(notification Notification) mergeKey {
	return mergeKey{notification.WorkspaceID, notification.AudienceKind, notification.AudienceID, notification.Kind, notification.ConversationID, notification.SenderSubjectID, notification.TokenSessionID, notification.NotificationID, notification.AgentID, notification.ChannelID}
}

// batch 按登记顺序保存一次事务内合并后的通知。
type batch struct {
	order []mergeKey
	items map[mergeKey]Notification
}

// RunInTx 经 serverstorage.RunInTx 执行写事务，事务内通过 Notify 登记的通知合并为一批，作为一个提交后回调交给发布器，唤醒信号另外直接送达本进程订阅，回滚则丢弃；
// db 为 bun.Tx 时按 serverstorage.RunInTx 的保存点规则加入外层事务，通知随外层事务提交发布。
func RunInTx(ctx context.Context, db bun.IDB, fn func(context.Context, bun.Tx) error) error {
	return serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		pending := &batch{items: map[mergeKey]Notification{}}
		serverstorage.AfterCommit(ctx, func(context.Context) error {
			deliver(pending.notifications())
			return nil
		})
		return fn(context.WithValue(ctx, batchKey{}, pending), tx)
	})
}

// notifications 按登记顺序返回批次中合并后的通知。
func (b *batch) notifications() []Notification {
	return arr.Map(b.order, func(key mergeKey) Notification { return b.items[key] })
}

// Notify 在当前 RunInTx 事务内登记通知，同一受众、种类和会话合并为最高版本并合并全部变化类别，版本相同时保留后到的通知；调用方必须处于 RunInTx 内。
func Notify(ctx context.Context, notification Notification) {
	pending, ok := ctx.Value(batchKey{}).(*batch)
	if !ok {
		panic("realtime: Notify called outside realtime.RunInTx")
	}
	key := notificationKey(notification)
	current, exists := pending.items[key]
	if !exists {
		pending.order = append(pending.order, key)
		pending.items[key] = notification
		return
	}
	changes := current.Changes | notification.Changes
	if notification.Version >= current.Version {
		current = notification
	}
	current.Changes = changes
	pending.items[key] = current
}

// IsAuthorizationChange 返回通知是否要求强制撤销已有实时授权。
func IsAuthorizationChange(kind Kind) bool {
	return kind == KindSessionLoggedOut || kind == KindUserDisabled || kind == KindWorkspaceStatusChanged || kind == KindMembershipAdded || kind == KindChannelDisabled || kind == KindCustomerIdentityRevoked || kind == KindComputerCredentialRevoked
}
