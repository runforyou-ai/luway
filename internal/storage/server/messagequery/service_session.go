//go:build server

package messagequery

import (
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// humanInvolvementEvents 是表示客服周期有真人参与处理的系统事件。
var humanInvolvementEvents = []domain.ConversationSystemEventType{
	domain.ConversationSystemEventServiceSessionHandedOff, domain.ConversationSystemEventServiceSessionReturned,
	domain.ConversationSystemEventServiceSessionClaimed, domain.ConversationSystemEventServiceSessionTakenOver,
	domain.ConversationSystemEventServiceSessionTransferred,
}

// AIOnly 返回客服周期由 AI 独立处理的条件：周期由 AI 员工关闭，且周期内没有转人工、退回队列、真人领取、接管、转交或真人对客回复；alias 为 service_sessions 的别名。
func AIOnly(alias string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`EXISTS (
	SELECT 1 FROM workspace_identities closer
	WHERE closer.workspace_id = ?.workspace_id AND closer.id = ?.closed_by_identity_id AND closer.type = ?
) AND NOT EXISTS (
	SELECT 1 FROM messages m
	LEFT JOIN conversation_participants cp ON cp.id = m.sender_participant_id AND cp.workspace_id = m.workspace_id
	LEFT JOIN chat_subjects cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?
	LEFT JOIN workspace_identities oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id
	WHERE m.workspace_id = ?.workspace_id AND m.service_session_id = ?.id
		AND (m.system_event_type IN (?) OR (m.visibility = ? AND m.type IN (?) AND oi.type = ?))
)`, name, name, domain.WorkspaceIdentityTypeAgent, domain.ChatSubjectKindWorkspaceIdentity, name, name,
		bun.List(humanInvolvementEvents), domain.MessageVisibilityShared,
		bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}), domain.WorkspaceIdentityTypeUser)
}

// RequesterSpoke 返回客服周期内有发起人对客文本或附件消息的条件；alias 为 service_sessions 的别名。
func RequesterSpoke(alias string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`EXISTS (
	SELECT 1 FROM messages m
	JOIN conversation_participants cp ON cp.id = m.sender_participant_id AND cp.workspace_id = m.workspace_id
	JOIN service_conversations svc ON svc.id = ?.service_conversation_id AND svc.workspace_id = ?.workspace_id
	WHERE m.workspace_id = ?.workspace_id AND m.service_session_id = ?.id AND cp.subject_id = svc.requester_subject_id
		AND m.visibility = ? AND m.type IN (?) AND m.deleted_at IS NULL
)`, name, name, name, name, domain.MessageVisibilityShared, bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}))
}

// AgentReplied 返回客服周期内有 AI 员工对客文本或附件消息的条件；alias 为 service_sessions 的别名。
func AgentReplied(alias string) schema.QueryWithArgs {
	return repliedBy(alias, domain.WorkspaceIdentityTypeAgent)
}

// HumanReplied 返回客服周期内有服务发起人以外的真人成员对客文本或附件消息的条件；alias 为 service_sessions 的别名。
func HumanReplied(alias string) schema.QueryWithArgs {
	return repliedBy(alias, domain.WorkspaceIdentityTypeUser)
}

// repliedBy 返回客服周期内有服务发起人以外、指定类型的工作区身份发出对客文本或附件消息的条件；alias 为 service_sessions 的别名。
func repliedBy(alias string, identityType domain.WorkspaceIdentityType) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`EXISTS (
	SELECT 1 FROM messages m
	JOIN conversation_participants cp ON cp.id = m.sender_participant_id AND cp.workspace_id = m.workspace_id
	JOIN chat_subjects cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?
	JOIN workspace_identities oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id AND oi.type = ?
	JOIN service_conversations svc ON svc.id = ?.service_conversation_id AND svc.workspace_id = ?.workspace_id
	WHERE m.workspace_id = ?.workspace_id AND m.service_session_id = ?.id AND cp.subject_id <> svc.requester_subject_id
		AND m.visibility = ? AND m.type IN (?) AND m.deleted_at IS NULL
)`, domain.ChatSubjectKindWorkspaceIdentity, identityType, name, name, name, name,
		domain.MessageVisibilityShared, bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}))
}
