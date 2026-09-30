//go:build server

package chatstate

// GroupMemberPreviewNamesLimit 是未命名群聊按成员拼接名称时取用的成员数。
const GroupMemberPreviewNamesLimit = 3

// GroupMemberPreviewNamesExpr 返回外层群聊 cv 中除查看者外按入群先后排列的前几名在群成员名称数组，占位参数依次为查看者企业身份编号和取用数量。
const GroupMemberPreviewNamesExpr = `ARRAY(
	SELECT preview_name_oi.display_name
	FROM conversation_participants AS preview_name_cp
	JOIN chat_subjects AS preview_name_cs ON preview_name_cs.organization_id = preview_name_cp.organization_id AND preview_name_cs.id = preview_name_cp.subject_id AND preview_name_cs.kind = 'organization_identity'
	JOIN organization_identities AS preview_name_oi ON preview_name_oi.organization_id = preview_name_cs.organization_id AND preview_name_oi.id = preview_name_cs.source_id
	WHERE preview_name_cp.organization_id = cv.organization_id AND preview_name_cp.conversation_id = cv.id
		AND preview_name_cp.left_at IS NULL AND preview_name_cs.source_id <> ?
	ORDER BY preview_name_cp.joined_at ASC, lower(preview_name_oi.display_name) ASC, preview_name_oi.id ASC
	LIMIT ?
)`
