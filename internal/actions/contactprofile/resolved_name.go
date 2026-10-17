//go:build server

package contactprofile

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// NameExpr 返回联系人在会话上下文中的 LIVE 名称表达式：档案名称、当前渠道身份名称、首选邮箱依次取第一个非空值，均为空时为 NULL。
// contact 是联系人表别名，identityName 是当前渠道身份名称的 SQL 表达式。
func NameExpr(contact, identityName string) string {
	return fmt.Sprintf(`COALESCE(NULLIF(btrim(%[1]s.display_name), ''), NULLIF(btrim(%[2]s), ''), %[1]s.primary_email)`, contact, identityName)
}

// ResolvedColumn 返回联系人目录 DENORM 名称列引用；contact 是联系人表别名。
func ResolvedColumn(contact string) string {
	return contact + ".resolved_display_name"
}

// LatestIdentityName 返回联系人最近更新且带名称的渠道身份名称的 SQL 表达式；contact 是联系人表别名。
func LatestIdentityName(contact string) string {
	return fmt.Sprintf(`(SELECT latest_cci.display_name FROM channel_identities AS latest_cci
		WHERE latest_cci.workspace_id = %[1]s.workspace_id AND latest_cci.contact_id = %[1]s.id AND btrim(latest_cci.display_name) <> ''
		ORDER BY latest_cci.updated_at DESC, latest_cci.id DESC LIMIT 1)`, contact)
}

// RefreshResolvedDisplayName 按档案名称、最近渠道身份名称、首选邮箱重写联系人的成员界面名称，并返回是否变化。
func RefreshResolvedDisplayName(ctx context.Context, db bun.IDB, workspaceID, contactID string) (bool, error) {
	resolved := NameExpr("c", LatestIdentityName("c"))
	result, err := db.NewRaw(`UPDATE contacts AS c SET resolved_display_name = `+resolved+`
		WHERE c.workspace_id = ? AND c.id = ? AND c.resolved_display_name IS DISTINCT FROM (`+resolved+`)`,
		workspaceID, contactID,
	).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("refresh contact resolved display name: %w", err)
	}
	return changed(result)
}

// SubjectName 返回聊天主体名称的 SQL 表达式：工作区身份取显示名称，联系人取 DENORM 成员界面名称；subjectID 是聊天主体编号的 SQL 表达式，为空时结果为 NULL。
// agentprocess 因依赖环不能引用本包，其 SubjectName 须与此公式保持一致。
func SubjectName(subjectID string) string {
	return fmt.Sprintf(`(SELECT CASE WHEN name_cs.kind = '%[2]s' THEN %[4]s ELSE name_oi.display_name END
		FROM chat_subjects AS name_cs
		LEFT JOIN workspace_identities AS name_oi ON name_oi.workspace_id = name_cs.workspace_id AND name_oi.id = name_cs.source_id AND name_cs.kind = '%[3]s'
		LEFT JOIN contacts AS name_c ON name_c.workspace_id = name_cs.workspace_id AND name_c.id = name_cs.source_id AND name_cs.kind = '%[2]s'
		WHERE name_cs.id = %[1]s)`, subjectID, domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity, ResolvedColumn("name_c"))
}

// KindResolvedName 返回已 JOIN 的聊天主体在 DENORM 语义下的展示名表达式：联系人取 resolved_display_name，否则取企业身份 display_name。
// kindExpr 是主体 kind 列，contactAlias / identityAlias 是已按 kind 关联的表别名。
func KindResolvedName(kindExpr, contactAlias, identityAlias string) string {
	return fmt.Sprintf("CASE WHEN %s = '%s' THEN %s ELSE %s.display_name END",
		kindExpr, domain.ChatSubjectKindContact, ResolvedColumn(contactAlias), identityAlias)
}

// RequesterNameExpr 返回发起人 LIVE 展示名称：联系人取档案名或当前渠道身份名，否则取企业身份显示名。
func RequesterNameExpr(contactAlias, channelIdentityName, workspaceIdentityAlias string) string {
	return fmt.Sprintf("COALESCE(%s, %s.display_name)", NameExpr(contactAlias, channelIdentityName), workspaceIdentityAlias)
}

// RequesterAvatarExpr 返回发起人头像文件编号表达式。
func RequesterAvatarExpr(channelIdentityAlias, workspaceIdentityAlias string) string {
	return fmt.Sprintf("COALESCE(%s.avatar_file_id, %s.avatar_file_id)::text", channelIdentityAlias, workspaceIdentityAlias)
}

// RequesterChannelJoins 从已关联的 service_conversations（别名 svc）拼出发起人主体、联系人/企业身份与渠道身份、渠道的 JOIN；依赖同层已关联的 service_sessions（别名 ss）。
func RequesterChannelJoins() (string, []any) {
	return `
JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.workspace_id = svc.workspace_id
LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?
LEFT JOIN workspace_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?
LEFT JOIN channel_conversations AS cc ON cc.conversation_id = svc.conversation_id AND cc.workspace_id = svc.workspace_id
LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id
LEFT JOIN channels AS ch ON ch.id = COALESCE(ss.channel_id, ci.channel_id) AND ch.workspace_id = svc.workspace_id
`, []any{domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity}
}
