//go:build server

package inbox

import (
	"github.com/runforyou-ai/cervi/internal/actions/contactname"
	"github.com/runforyou-ai/cervi/internal/common"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// serviceConversationAccessQuery 共用服务会话阅读范围和公开摘要所需的有效关联；发起人按聊天主体关联联系人或企业身份，渠道只对渠道来源存在；活动时间计入内部消息，末条消息取查看者可见的最后一条。
func (q *LoadInboxQuery) serviceConversationAccessQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.db.NewSelect().TableExpr("service_conversations AS svc").
		ColumnExpr("cv.id, GREATEST(cv.last_activity_at, cv.last_internal_activity_at) AS last_activity_at").
		Join("JOIN conversations AS cv ON cv.id = svc.conversation_id AND cv.organization_id = svc.organization_id").
		Join("JOIN service_sessions AS current ON current.organization_id = svc.organization_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.organization_id = svc.organization_id").
		Join("LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.organization_id = requester_cs.organization_id AND requester_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN organization_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.organization_id = requester_cs.organization_id AND requester_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = svc.conversation_id AND cc.organization_id = svc.organization_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Join("LEFT JOIN channels AS ch ON ch.id = cci.channel_id AND ch.organization_id = cci.organization_id").
		Join("LEFT JOIN messages AS msg ON msg.id = cv.last_message_id AND msg.organization_id = cv.organization_id AND msg.conversation_id = cv.id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN LATERAL (?) AS last_visible ON TRUE", lastVisibleMessageQuery(q.db, identityID)).
		Where("svc.organization_id = ?", organizationID)
}

// memberConversationAccessQuery 按当前有效成员关系读取活跃或归档的内部会话。
func (q *LoadInboxQuery) memberConversationAccessQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.db.NewSelect().TableExpr("conversations AS cv").
		ColumnExpr("cv.id, cv.last_activity_at").
		Join("JOIN conversation_participants AS mine ON mine.organization_id = cv.organization_id AND mine.conversation_id = cv.id AND mine.left_at IS NULL").
		Join("JOIN chat_subjects AS mine_cs ON mine_cs.id = mine.subject_id AND mine_cs.organization_id = mine.organization_id AND mine_cs.kind = ? AND mine_cs.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identityID).
		Where("cv.organization_id = ?", organizationID).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived)
}

// directConversationAccessQuery 以真人身份对和有效成员关系限定阅读范围。
func (q *LoadInboxQuery) directConversationAccessQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.memberConversationAccessQuery(organizationID, identityID).
		Join("JOIN direct_conversations AS dc ON dc.organization_id = cv.organization_id AND dc.conversation_id = cv.id").
		Join("JOIN organization_identities AS peer_oi ON peer_oi.organization_id = dc.organization_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identityID).
		Join("JOIN users AS peer_u ON peer_u.organization_id = peer_oi.organization_id AND peer_u.identity_id = peer_oi.id").
		Where("cv.type = ?", domain.ConversationTypeDirect).
		Where("? IN (dc.first_identity_id, dc.second_identity_id)", identityID).
		Where("peer_oi.type = ?", domain.OrganizationIdentityTypeUser)
}

// agentConversationAccessQuery 以固定业务归属和有效成员关系限定 AI 会话阅读范围。
func (q *LoadInboxQuery) agentConversationAccessQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.memberConversationAccessQuery(organizationID, identityID).
		Join("JOIN agent_conversations AS ac ON ac.organization_id = cv.organization_id AND ac.conversation_id = cv.id").
		Join("JOIN organization_identities AS oi ON oi.organization_id = ac.organization_id AND oi.id = ac.agent_identity_id").
		Join("JOIN agents AS agent ON agent.organization_id = oi.organization_id AND agent.identity_id = oi.id").
		Where("cv.type = ? AND ac.user_identity_id = ? AND oi.type IN (?, ?)", domain.ConversationTypeAgent, identityID, domain.OrganizationIdentityTypeAgent, domain.OrganizationIdentityTypeAssistant)
}

// groupConversationAccessQuery 限定当前成员可读的群聊，解散后仍保留阅读资格。
func (q *LoadInboxQuery) groupConversationAccessQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.memberConversationAccessQuery(organizationID, identityID).Where("cv.type = ?", domain.ConversationTypeGroup)
}

// directConversationsQuery 限定当前列表中会话状态为活跃的真人单聊。
func (q *LoadInboxQuery) directConversationsQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.directConversationAccessQuery(organizationID, identityID).Where("cv.status = ?", domain.ConversationStatusActive)
}

// agentConversationsQuery 限定当前列表中会话状态为活跃的 AI 聊天。
func (q *LoadInboxQuery) agentConversationsQuery(organizationID, identityID string) *bun.SelectQuery {
	return q.agentConversationAccessQuery(organizationID, identityID).Where("cv.status = ?", domain.ConversationStatusActive)
}

// listCandidates 共用列表分页与按 ID 资格判断的最小候选投影；带搜索词时按范围取候选后再匹配会话名称。
func (q *LoadInboxQuery) listCandidates(identity *servermodels.Identity, input LoadInput) *bun.SelectQuery {
	if input.SearchRange == SearchRangeReadable {
		return q.matchConversationNames(identity, q.readableCandidates(identity), input.Search)
	}
	organizationID, identityID := identity.Organization.ID, identity.OrganizationIdentity.ID
	var candidate *bun.SelectQuery
	switch input.Scope {
	case domain.InboxScopePending:
		candidate = q.pendingCandidates(identity, input)
	case domain.InboxScopeAll:
		candidate = filterServiceInbox(q.serviceConversationAccessQuery(organizationID, identityID), input)
	default:
		queries := make([]*bun.SelectQuery, 0, 3)
		if input.includesKind(domain.ConversationTypeDirect) {
			queries = append(queries, q.directConversationsQuery(organizationID, identityID))
		}
		if input.includesKind(domain.ConversationTypeAgent) {
			queries = append(queries, q.agentConversationsQuery(organizationID, identityID))
		}
		if input.includesKind(domain.ConversationTypeGroup) {
			queries = append(queries, q.groupConversationAccessQuery(organizationID, identityID))
		}
		candidate = queries[0]
		for _, query := range queries[1:] {
			candidate = candidate.UnionAll(query)
		}
	}
	if input.Search != "" {
		return q.matchConversationNames(identity, candidate, input.Search)
	}
	return candidate
}

// pendingCandidates 读取本人待处理的服务会话，投影条目类型、等待起点与是否有未回应的提醒；筛选 @我 时等待起点取提醒时间。
func (q *LoadInboxQuery) pendingCandidates(identity *servermodels.Identity, input LoadInput) *bun.SelectQuery {
	identityID := identity.OrganizationIdentity.ID
	items := filterServiceInbox(q.serviceConversationAccessQuery(identity.Organization.ID, identityID), input).
		Where("current.status = ?", domain.ServiceSessionStatusOpen).
		// 当前周期内提醒本人、且本人之后尚未在会话中发言的最早一条内部备注时间。
		Join(`LEFT JOIN LATERAL (
			SELECT min(note.originated_at) AS mentioned_at
			FROM messages AS note
			JOIN message_mentions AS note_mention ON note_mention.organization_id = note.organization_id AND note_mention.message_id = note.id
			JOIN chat_subjects AS mentioned_cs ON mentioned_cs.organization_id = note_mention.organization_id AND mentioned_cs.id = note_mention.subject_id
			WHERE note.organization_id = cv.organization_id AND note.conversation_id = cv.id AND note.service_session_id = current.id AND note.deleted_at IS NULL
				AND mentioned_cs.kind = ? AND mentioned_cs.source_id = ?
				AND NOT EXISTS (
					SELECT 1 FROM messages AS answer
					JOIN conversation_participants AS answer_cp ON answer_cp.organization_id = answer.organization_id AND answer_cp.conversation_id = answer.conversation_id AND answer_cp.id = answer.sender_participant_id
					WHERE answer.organization_id = note.organization_id AND answer.conversation_id = note.conversation_id
						AND answer.message_seq > note.message_seq AND answer.deleted_at IS NULL AND answer_cp.subject_id = note_mention.subject_id
				)
		) AS my_mention ON TRUE`, domain.ChatSubjectKindOrganizationIdentity, identityID).
		// 依次取第一个成立的类型：本人负责且客户正在等待、公共队列或本人团队队列中无人负责、内部备注提醒本人未回应。
		ColumnExpr(`CASE
			WHEN current.assignee_identity_id = ? AND current.awaiting_reply_since IS NOT NULL THEN ?
			WHEN current.assignee_identity_id IS NULL AND (current.team_id IS NULL OR EXISTS (
				SELECT 1 FROM team_members AS tm WHERE tm.organization_id = current.organization_id AND tm.team_id = current.team_id AND tm.identity_id = ?
			)) THEN ?
			WHEN my_mention.mentioned_at IS NOT NULL THEN ?
		END AS pending_kind`, identityID, domain.InboxPendingKindReply, identityID, domain.InboxPendingKindQueue, domain.InboxPendingKindMention).
		// 等我回复从客户开始等待与本人获得周期的较晚者计起，待领取从客户开始等待与进入队列的较早者计起。
		ColumnExpr("GREATEST(current.awaiting_reply_since, current.assignee_assigned_at) AS reply_since").
		ColumnExpr("LEAST(current.awaiting_reply_since, current.queued_at) AS queue_since").
		ColumnExpr("my_mention.mentioned_at").
		ColumnExpr("current.team_id AS queue_team_id")
	// 等待起点按条目类型取值；筛选 @我 时取最早一条未回应提醒的时间。
	waitingSince := bun.SafeQuery("CASE items.pending_kind WHEN ? THEN items.reply_since WHEN ? THEN items.queue_since ELSE items.mentioned_at END", domain.InboxPendingKindReply, domain.InboxPendingKindQueue)
	if input.PendingKind == domain.InboxPendingKindMention {
		waitingSince = bun.SafeQuery("items.mentioned_at")
	}
	query := q.db.NewSelect().TableExpr("(?) AS items", items).
		ColumnExpr("items.id, items.last_activity_at, items.pending_kind, items.mentioned_at IS NOT NULL AS mentioned").
		ColumnExpr("? AS waiting_since", waitingSince).
		Where("items.pending_kind IS NOT NULL")
	// 内部备注提醒本人独立于条目类型，筛选 @我 时包含同时等我回复或待领取的会话。
	switch input.PendingKind {
	case "":
	case domain.InboxPendingKindMention:
		query = query.Where("items.mentioned_at IS NOT NULL")
	default:
		query = query.Where("items.pending_kind = ?", input.PendingKind)
	}
	switch input.QueueFilter {
	case domain.ServiceQueueFilterPublic:
		query = query.Where("items.queue_team_id IS NULL")
	case domain.ServiceQueueFilterTeam:
		query = query.Where("items.queue_team_id = ?", input.QueueTeamID)
	}
	return query
}

// matchConversationNames 按会话名称筛选候选：群聊匹配群名，未命名的群匹配除查看者外任一在群成员的名称，单聊匹配对方名称，AI 聊天匹配标题或 AI 名称，他人发起的 AI 聊天服务会话另外匹配发起人名称，客户会话匹配客户在成员界面的名称或以检索词末尾编号匹配联系人编号；名称与搜索词同样经 NFKC 规范化并合并连续空白，搜索词中的通配符按字面匹配，返回与候选相同的投影。
func (q *LoadInboxQuery) matchConversationNames(identity *servermodels.Identity, candidates *bun.SelectQuery, search string) *bun.SelectQuery {
	pattern := common.ContainsPattern(search)
	// 名称按搜索词的规则规范化后再匹配。
	name := func(column string) string {
		return "regexp_replace(normalize(" + column + ", NFKC), '[[:space:]]+', ' ', 'g')"
	}
	return q.db.NewSelect().TableExpr("(?) AS candidates", candidates).
		ColumnExpr("candidates.*").
		Join("JOIN conversations AS cv ON cv.organization_id = ? AND cv.id = candidates.id", identity.Organization.ID).
		Join("LEFT JOIN direct_conversations AS dc ON dc.organization_id = cv.organization_id AND dc.conversation_id = cv.id").
		Join("LEFT JOIN organization_identities AS peer_oi ON peer_oi.organization_id = dc.organization_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identity.OrganizationIdentity.ID).
		Join("LEFT JOIN agent_conversations AS ac ON ac.organization_id = cv.organization_id AND ac.conversation_id = cv.id").
		Join("LEFT JOIN organization_identities AS agent_oi ON agent_oi.organization_id = ac.organization_id AND agent_oi.id = ac.agent_identity_id").
		Join("LEFT JOIN organization_identities AS agent_user_oi ON agent_user_oi.organization_id = ac.organization_id AND agent_user_oi.id = ac.user_identity_id AND ac.user_identity_id <> ?", identity.OrganizationIdentity.ID).
		Join("LEFT JOIN channel_conversations AS cc ON cc.organization_id = cv.organization_id AND cc.conversation_id = cv.id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Join("LEFT JOIN contacts AS c ON c.organization_id = cci.organization_id AND c.id = cci.contact_id").
		// 未命名的群按除查看者外的在群成员名称匹配。
		Where(`(cv.type = ? AND (`+name("cv.title")+` ILIKE ? OR (cv.title IS NULL AND EXISTS (
				SELECT 1 FROM conversation_participants AS member_cp
				JOIN chat_subjects AS member_cs ON member_cs.organization_id = member_cp.organization_id AND member_cs.id = member_cp.subject_id AND member_cs.kind = ?
				JOIN organization_identities AS member_oi ON member_oi.organization_id = member_cs.organization_id AND member_oi.id = member_cs.source_id
				WHERE member_cp.organization_id = cv.organization_id AND member_cp.conversation_id = cv.id AND member_cp.left_at IS NULL
					AND member_cs.source_id <> ? AND `+name("member_oi.display_name")+` ILIKE ?))))
			OR (cv.type = ? AND `+name("peer_oi.display_name")+` ILIKE ?)
			OR (cv.type = ? AND (`+name("cv.title")+` ILIKE ? OR `+name("agent_oi.display_name")+` ILIKE ? OR `+name("agent_user_oi.display_name")+` ILIKE ?))
			OR (cv.type = ? AND (`+name(contactname.Expr("c", "cci.display_name"))+` ILIKE ? OR c.number = ?))`,
			domain.ConversationTypeGroup, pattern, domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID, pattern,
			domain.ConversationTypeDirect, pattern,
			domain.ConversationTypeAgent, pattern, pattern, pattern, domain.ConversationTypeChannel, pattern, contactname.Number(search))
}
