//go:build server

package inbox

import (
	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// listable 返回查看者可在收件箱中展示指定类别会话 cv 的条件。
func listable(workspaceID, identityID sqlArg, kind conversationaccess.Kind) schema.QueryWithArgs {
	return conversationaccess.Listable(conversationaccess.Viewer{WorkspaceID: workspaceID, IdentityID: identityID}, "cv", kind)
}

// serviceConversationAccessQuery 共用服务会话列表范围和公开摘要所需的有效关联；发起人按聊天主体关联联系人或企业身份，渠道只对渠道来源存在；活动时间计入内部消息，末条消息取查看者可见的最后一条。
func (q *LoadInboxQuery) serviceConversationAccessQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.db.NewSelect().TableExpr("service_conversations AS svc").
		ColumnExpr("cv.id, GREATEST(cv.last_activity_at, cv.last_internal_activity_at) AS last_activity_at").
		Join("JOIN conversations AS cv ON cv.id = svc.conversation_id AND cv.workspace_id = svc.workspace_id").
		Join("JOIN service_sessions AS current ON current.workspace_id = svc.workspace_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.workspace_id = svc.workspace_id").
		Join("LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN workspace_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = svc.conversation_id AND cc.workspace_id = svc.workspace_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("LEFT JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Join("LEFT JOIN messages AS msg ON msg.id = cv.last_message_id AND msg.workspace_id = cv.workspace_id AND msg.conversation_id = cv.id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN LATERAL (?) AS last_visible ON TRUE", lastVisibleMessageQuery(q.db, identityID)).
		Where("svc.workspace_id = ?", workspaceID).
		Where("?", listable(workspaceID, identityID, conversationaccess.KindService))
}

// memberConversationAccessQuery 读取查看者可在收件箱中展示的指定类别活跃或归档内部会话；可展示的会话先在派生表 cv 中筛出，查询由本人成员关系驱动。
func (q *LoadInboxQuery) memberConversationAccessQuery(workspaceID, identityID sqlArg, kind conversationaccess.Kind) *bun.SelectQuery {
	listed := q.db.NewSelect().TableExpr("conversations AS cv").ColumnExpr("cv.*").
		Where("cv.workspace_id = ?", workspaceID).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived).
		Where("?", listable(workspaceID, identityID, kind))
	return q.db.NewSelect().TableExpr("(?) AS cv", listed).ColumnExpr("cv.id, cv.last_activity_at")
}

// directConversationAccessQuery 读取可展示的单聊并关联对方真人成员。
func (q *LoadInboxQuery) directConversationAccessQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.memberConversationAccessQuery(workspaceID, identityID, conversationaccess.KindDirect).
		Join("JOIN direct_conversations AS dc ON dc.workspace_id = cv.workspace_id AND dc.conversation_id = cv.id").
		Join("JOIN workspace_identities AS peer_oi ON peer_oi.workspace_id = dc.workspace_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identityID).
		Join("JOIN users AS peer_u ON peer_u.workspace_id = peer_oi.workspace_id AND peer_u.identity_id = peer_oi.id")
}

// agentConversationAccessQuery 读取可展示的 AI 员工会话并关联 AI 员工。
func (q *LoadInboxQuery) agentConversationAccessQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.memberConversationAccessQuery(workspaceID, identityID, conversationaccess.KindAgent).
		Join("JOIN agent_conversations AS ac ON ac.workspace_id = cv.workspace_id AND ac.conversation_id = cv.id").
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = ac.workspace_id AND oi.id = ac.agent_identity_id").
		Join("JOIN agents AS agent ON agent.workspace_id = oi.workspace_id AND agent.identity_id = oi.id")
}

// groupConversationAccessQuery 读取可展示的群聊，解散后仍保留。
func (q *LoadInboxQuery) groupConversationAccessQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.memberConversationAccessQuery(workspaceID, identityID, conversationaccess.KindGroup)
}

// directConversationsQuery 限定当前列表中会话状态为活跃的真人单聊。
func (q *LoadInboxQuery) directConversationsQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.directConversationAccessQuery(workspaceID, identityID).Where("cv.status = ?", domain.ConversationStatusActive)
}

// agentConversationsQuery 限定当前列表中会话状态为活跃的 AI 聊天。
func (q *LoadInboxQuery) agentConversationsQuery(workspaceID, identityID sqlArg) *bun.SelectQuery {
	return q.agentConversationAccessQuery(workspaceID, identityID).Where("cv.status = ?", domain.ConversationStatusActive)
}

// listCandidates 共用列表分页与按 ID 资格判断的最小候选投影；带搜索词时按范围取候选后再匹配会话名称。
func (q *LoadInboxQuery) listCandidates(identity *servermodels.Identity, input LoadInput) *bun.SelectQuery {
	if input.SearchRange == SearchRangeReadable {
		return q.matchConversationNames(identity, q.readableCandidates(identity), input.Search)
	}
	workspaceID, identityID := identity.Workspace.ID, identity.WorkspaceIdentity.ID
	var candidate *bun.SelectQuery
	switch input.Scope {
	case domain.InboxScopePending:
		candidate = q.pendingCandidates(workspaceID, identityID, input)
	case domain.InboxScopeAll:
		candidate = filterServiceInbox(q.serviceConversationAccessQuery(workspaceID, identityID), input)
	default:
		queries := make([]*bun.SelectQuery, 0, 3)
		if input.includesKind(domain.ConversationTypeDirect) {
			queries = append(queries, q.directConversationsQuery(workspaceID, identityID))
		}
		if input.includesKind(domain.ConversationTypeAgent) {
			queries = append(queries, q.agentConversationsQuery(workspaceID, identityID))
		}
		if input.includesKind(domain.ConversationTypeGroup) {
			queries = append(queries, q.groupConversationAccessQuery(workspaceID, identityID))
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
func (q *LoadInboxQuery) pendingCandidates(workspaceID, identityID sqlArg, input LoadInput) *bun.SelectQuery {
	items := filterServiceInbox(q.serviceConversationAccessQuery(workspaceID, identityID), input).
		Where("current.status = ?", domain.ServiceSessionStatusOpen).
		// 当前周期内提醒本人、且本人之后尚未在会话中发言的最早一条内部备注时间。
		Join(`LEFT JOIN LATERAL (
			SELECT min(note.originated_at) AS mentioned_at
			FROM messages AS note
			JOIN message_mentions AS note_mention ON note_mention.workspace_id = note.workspace_id AND note_mention.message_id = note.id
			JOIN chat_subjects AS mentioned_cs ON mentioned_cs.workspace_id = note_mention.workspace_id AND mentioned_cs.id = note_mention.subject_id
			WHERE note.workspace_id = cv.workspace_id AND note.conversation_id = cv.id AND note.service_session_id = current.id AND note.deleted_at IS NULL
				AND mentioned_cs.kind = ? AND mentioned_cs.source_id = ?
				AND NOT EXISTS (
					SELECT 1 FROM messages AS answer
					JOIN conversation_participants AS answer_cp ON answer_cp.workspace_id = answer.workspace_id AND answer_cp.conversation_id = answer.conversation_id AND answer_cp.id = answer.sender_participant_id
					WHERE answer.workspace_id = note.workspace_id AND answer.conversation_id = note.conversation_id
						AND answer.message_seq > note.message_seq AND answer.deleted_at IS NULL AND answer_cp.subject_id = note_mention.subject_id
				)
		) AS my_mention ON TRUE`, domain.ChatSubjectKindWorkspaceIdentity, identityID).
		// 依次取第一个成立的类型：本人负责且客户正在等待、公共队列或本人团队队列中无人负责、内部备注提醒本人未回应。
		ColumnExpr(`CASE
			WHEN current.assignee_identity_id = ? AND current.awaiting_reply_since IS NOT NULL THEN ?
			WHEN current.assignee_identity_id IS NULL AND (current.team_id IS NULL OR EXISTS (
				SELECT 1 FROM team_members AS tm WHERE tm.workspace_id = current.workspace_id AND tm.team_id = current.team_id AND tm.identity_id = ?
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

// matchConversationNames 按会话名称筛选候选：群聊匹配群名，未命名的群匹配除查看者外任一在群成员的名称，单聊匹配对方名称，AI 聊天匹配标题或 AI 名称，他人发起的 AI 聊天服务会话另外匹配发起人名称，渠道会话匹配客户在成员界面的名称、以检索词末尾编号匹配联系人编号或匹配发起成员的名称；名称与搜索词同样经 NFKC 规范化并合并连续空白，搜索词中的通配符按字面匹配，返回与候选相同的投影。
func (q *LoadInboxQuery) matchConversationNames(identity *servermodels.Identity, candidates *bun.SelectQuery, search string) *bun.SelectQuery {
	pattern := common.ContainsPattern(search)
	// 名称按搜索词的规则规范化后再匹配。
	name := func(column string) string {
		return "regexp_replace(normalize(" + column + ", NFKC), '[[:space:]]+', ' ', 'g')"
	}
	return q.db.NewSelect().TableExpr("(?) AS candidates", candidates).
		ColumnExpr("candidates.*").
		Join("JOIN conversations AS cv ON cv.workspace_id = ? AND cv.id = candidates.id", identity.Workspace.ID).
		Join("LEFT JOIN direct_conversations AS dc ON dc.workspace_id = cv.workspace_id AND dc.conversation_id = cv.id").
		Join("LEFT JOIN workspace_identities AS peer_oi ON peer_oi.workspace_id = dc.workspace_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identity.WorkspaceIdentity.ID).
		Join("LEFT JOIN agent_conversations AS ac ON ac.workspace_id = cv.workspace_id AND ac.conversation_id = cv.id").
		Join("LEFT JOIN workspace_identities AS agent_oi ON agent_oi.workspace_id = ac.workspace_id AND agent_oi.id = ac.agent_identity_id").
		Join("LEFT JOIN workspace_identities AS agent_user_oi ON agent_user_oi.workspace_id = ac.workspace_id AND agent_user_oi.id = ac.user_identity_id AND ac.user_identity_id <> ?", identity.WorkspaceIdentity.ID).
		Join("LEFT JOIN channel_conversations AS cc ON cc.workspace_id = cv.workspace_id AND cc.conversation_id = cv.id").
		Join("LEFT JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
		Join("LEFT JOIN contacts AS c ON c.workspace_id = ci.workspace_id AND c.id = ci.contact_id").
		Join("LEFT JOIN service_conversations AS channel_svc ON channel_svc.workspace_id = cc.workspace_id AND channel_svc.conversation_id = cc.conversation_id").
		Join("LEFT JOIN chat_subjects AS channel_requester_cs ON channel_requester_cs.workspace_id = channel_svc.workspace_id AND channel_requester_cs.id = channel_svc.requester_subject_id AND channel_requester_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN workspace_identities AS channel_requester_oi ON channel_requester_oi.workspace_id = channel_requester_cs.workspace_id AND channel_requester_oi.id = channel_requester_cs.source_id").
		// 未命名的群按除查看者外的在群成员名称匹配。
		Where(`(cv.type = ? AND (`+name("cv.title")+` ILIKE ? OR (cv.title IS NULL AND EXISTS (
				SELECT 1 FROM conversation_participants AS member_cp
				JOIN chat_subjects AS member_cs ON member_cs.workspace_id = member_cp.workspace_id AND member_cs.id = member_cp.subject_id AND member_cs.kind = ?
				JOIN workspace_identities AS member_oi ON member_oi.workspace_id = member_cs.workspace_id AND member_oi.id = member_cs.source_id
				WHERE member_cp.workspace_id = cv.workspace_id AND member_cp.conversation_id = cv.id AND member_cp.left_at IS NULL
					AND member_cs.source_id <> ? AND `+name("member_oi.display_name")+` ILIKE ?))))
			OR (cv.type = ? AND `+name("peer_oi.display_name")+` ILIKE ?)
			OR (cv.type = ? AND (`+name("cv.title")+` ILIKE ? OR `+name("agent_oi.display_name")+` ILIKE ? OR `+name("agent_user_oi.display_name")+` ILIKE ?))
			OR (cv.type = ? AND (`+name(contactname.Expr("c", "ci.display_name"))+` ILIKE ? OR c.number = ? OR `+name("channel_requester_oi.display_name")+` ILIKE ?))`,
			domain.ConversationTypeGroup, pattern, domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID, pattern,
			domain.ConversationTypeDirect, pattern,
			domain.ConversationTypeAgent, pattern, pattern, pattern, domain.ConversationTypeChannel, pattern, contactname.Number(search), pattern)
}
