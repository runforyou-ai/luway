//go:build server

// Package messagequery 提供消息查询共用的展示表达式、可见范围条件与客服周期参与情况条件。
package messagequery

import (
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// Summary 返回正文优先、无正文附件使用文件名、已删除消息为空的摘要。
func Summary(alias string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`CASE WHEN ?.deleted_at IS NOT NULL THEN ''
 WHEN ?.type = ? AND ?.body = '' THEN (
  SELECT ma.name FROM message_attachments AS ma WHERE ma.message_id = ?.id AND ma.organization_id = ?.organization_id
 ) ELSE ?.body END`, name, name, domain.MessageTypeAttachment, name, name, name, name)
}

// VisibleTo 返回别名消息对指定企业身份可见的条件：服务会话发起人看到共享消息与发给发起人的消息，其余读者看到共享消息与内部消息。
func VisibleTo(alias, identityID string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`CASE WHEN EXISTS (
  SELECT 1 FROM service_conversations AS visible_svc
  JOIN chat_subjects AS visible_cs ON visible_cs.organization_id = visible_svc.organization_id AND visible_cs.id = visible_svc.requester_subject_id
  WHERE visible_svc.organization_id = ?.organization_id AND visible_svc.conversation_id = ?.conversation_id AND visible_cs.kind = ? AND visible_cs.source_id = ?
 ) THEN ?.visibility IN (?, ?) ELSE ?.visibility IN (?, ?) END`,
		name, name, domain.ChatSubjectKindOrganizationIdentity, identityID,
		name, domain.MessageVisibilityShared, domain.MessageVisibilityRequester,
		name, domain.MessageVisibilityShared, domain.MessageVisibilityInternal)
}
