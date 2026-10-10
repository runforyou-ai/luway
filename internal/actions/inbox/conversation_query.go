//go:build server

package inbox

import (
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// serviceConversationDetailsQuery 读取企业内服务会话摘要，不按处理队列限制阅读；预览取当前成员可见的最后一条消息。
func (q *LoadInboxQuery) serviceConversationDetailsQuery(workspaceID, currentIdentityID, userID sqlArg) *bun.SelectQuery {
	return q.serviceConversationAccessQuery(workspaceID, currentIdentityID).
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("unread.mentioned_unread_count AS mentioned_unread_count").
		ColumnExpr("unanswered.unanswered_mention_count AS unanswered_mention_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("cv.type AS type").
		ColumnExpr("cv.title AS title").
		ColumnExpr("svc.source, svc.audience").
		// 发起人是成员时取成员名称与头像，是联系人时取联系人在成员界面的名称与渠道头像。
		ColumnExpr("COALESCE(requester_oi.display_name, "+contactname.Expr("c", "ci.display_name")+") AS requester_name").
		ColumnExpr("c.number AS requester_contact_number").
		ColumnExpr("COALESCE(requester_oi.avatar_file_id, ci.avatar_file_id)::text AS requester_avatar_file_id").
		ColumnExpr("svc.requester_subject_id::text AS requester_chat_subject_id").
		ColumnExpr("ch.type AS channel_type").
		ColumnExpr("ch.name AS channel_name").
		ColumnExpr("reply_window.expires_at AS reply_window_expires_at, reply_window.remaining AS reply_window_remaining").
		ColumnExpr("service_agent.id::text AS service_agent_identity_id, service_agent.display_name AS service_agent_name").
		ColumnExpr("? AS preview", messagequery.Summary("preview_msg")).
		ColumnExpr("preview_msg.type AS last_message_type, preview_msg.system_event_type AS last_system_event_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("preview_msg.visibility AS preview_visibility").
		ColumnExpr("last_visible.originated_at AS last_message_at").
		ColumnExpr("last_visible.id::text AS last_message_id").
		ColumnExpr("current.status AS service_session_status").
		ColumnExpr("current.id::text AS service_session_id").
		ColumnExpr("current.assignee_identity_id::text AS assignee_identity_id").
		ColumnExpr("assignee.type AS assignee_type").
		ColumnExpr("assignee.display_name AS assignee_display_name").
		ColumnExpr("(SELECT assignee_cs.id::text FROM chat_subjects AS assignee_cs WHERE assignee_cs.workspace_id = current.workspace_id AND assignee_cs.kind = ? AND assignee_cs.source_id = current.assignee_identity_id) AS assignee_chat_subject_id", domain.ChatSubjectKindWorkspaceIdentity).
		ColumnExpr("assignee.avatar_file_id::text AS assignee_avatar_file_id").
		ColumnExpr("current.team_id::text AS team_id").
		ColumnExpr("team.name AS team_name").
		Join("LEFT JOIN messages AS preview_msg ON preview_msg.workspace_id = cv.workspace_id AND preview_msg.conversation_id = cv.id AND preview_msg.id = last_visible.id AND preview_msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = preview_msg.sender_participant_id AND preview_cp.workspace_id = preview_msg.workspace_id AND preview_cp.conversation_id = preview_msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN workspace_identities AS assignee ON assignee.workspace_id = cv.workspace_id AND assignee.id = current.assignee_identity_id").
		Join("LEFT JOIN agent_conversations AS service_ac ON service_ac.workspace_id = cv.workspace_id AND service_ac.conversation_id = cv.id").
		Join("LEFT JOIN workspace_identities AS service_agent ON service_agent.workspace_id = service_ac.workspace_id AND service_agent.id = service_ac.agent_identity_id").
		Join("LEFT JOIN teams AS team ON team.workspace_id = cv.workspace_id AND team.id = current.team_id").
		// 发起人渠道身份可用的回复窗口取到期最晚的一个。
		Join(`LEFT JOIN LATERAL (
			SELECT crw.expires_at, crw.quota - crw.used - crw.reserved AS remaining
			FROM channel_reply_windows AS crw
			WHERE crw.workspace_id = ci.workspace_id AND crw.channel_identity_id = ci.id AND ?
			ORDER BY crw.expires_at DESC LIMIT 1
		) AS reply_window ON TRUE`, deliveryaction.UsableReplyWindowCondition("crw", 1)).
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, currentIdentityID)).
		// 统计当前周期内被提醒成员之后尚未在会话中发言的提醒。
		Join(`JOIN LATERAL (
			SELECT count(*) AS unanswered_mention_count
			FROM message_mentions AS note_mention
			JOIN messages AS note ON note.workspace_id = note_mention.workspace_id AND note.id = note_mention.message_id
			WHERE note.workspace_id = cv.workspace_id AND note.conversation_id = cv.id AND note.service_session_id = current.id AND note.deleted_at IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM messages AS answer
					JOIN conversation_participants AS answer_cp ON answer_cp.workspace_id = answer.workspace_id AND answer_cp.conversation_id = answer.conversation_id AND answer_cp.id = answer.sender_participant_id
					WHERE answer.workspace_id = note.workspace_id AND answer.conversation_id = note.conversation_id
						AND answer.message_seq > note.message_seq AND answer.deleted_at IS NULL AND answer_cp.subject_id = note_mention.subject_id
				)
		) AS unanswered ON TRUE`)
}

// filterServiceInbox 为服务会话追加来源与服务对象筛选；全部范围另按服务状态和负责人筛选。
func filterServiceInbox(query *bun.SelectQuery, input LoadInput) *bun.SelectQuery {
	query = query.Where("msg.id IS NOT NULL")
	if input.ChannelID != "" {
		query = query.Where("ci.channel_id = ?", input.ChannelID)
	}
	if input.Source != "" {
		query = query.Where("svc.source = ?", input.Source)
	}
	if input.Audience != "" {
		query = query.Where("svc.audience = ?", input.Audience)
	}
	if input.Scope != domain.InboxScopeAll {
		return query
	}
	query = query.Where("current.status = ?", input.ServiceStatus)
	switch input.AssigneeFilter {
	case domain.InboxAssigneeFilterUnassigned:
		query = query.Where("current.assignee_identity_id IS NULL")
	case domain.InboxAssigneeFilterIdentity:
		query = query.Where("current.assignee_identity_id = ?", input.AssigneeIdentityID)
	}
	return query
}

// withIndividualConversationDetails 为单聊阅读基线追加消息预览和个人未读状态；预览取会话末条消息，服务会话的末条消息只含发起人可见的消息。
func withIndividualConversationDetails(query *bun.SelectQuery, identityID, userID sqlArg) *bun.SelectQuery {
	return query.
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("msg.type AS last_message_type, msg.system_event_type AS last_system_event_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_message_id::text AS last_message_id").
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("COALESCE(state.marked_unread, false) AS marked_unread").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("state.archived_at").
		Join("LEFT JOIN messages AS msg ON msg.workspace_id = cv.workspace_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.workspace_id = msg.workspace_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(query.DB(), identityID))
}

// lastVisibleMessageQuery 构造会话 cv 中指定成员可见的最后一条消息，服务周期的系统事件只取发给发起人的服务进度。
func lastVisibleMessageQuery(db bun.IDB, identityID sqlArg) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS visible").
		ColumnExpr("visible.id, visible.originated_at").
		Where("visible.workspace_id = cv.workspace_id AND visible.conversation_id = cv.id").
		Where("visible.type <> ? OR visible.service_session_id IS NULL OR visible.visibility = ?", domain.MessageTypeSystem, domain.MessageVisibilityRequester).
		Where("?", messagequery.VisibleTo("visible", identityID)).
		OrderExpr("visible.message_seq DESC").
		Limit(1)
}

// directConversationDetailsQuery 按真人身份对及有效成员关系读取长期单聊。
func (q *LoadInboxQuery) directConversationDetailsQuery(workspaceID, identityID, userID sqlArg) *bun.SelectQuery {
	return withIndividualConversationDetails(q.directConversationAccessQuery(workspaceID, identityID), identityID, userID).
		ColumnExpr("peer_oi.id AS peer_identity_id, peer_oi.type AS peer_type, peer_oi.display_name AS peer_name, peer_oi.avatar_file_id AS peer_avatar_file_id, peer_u.status AS peer_status, peer_oi.work_status AS peer_work_status")
}

// agentConversationDetailsQuery 按业务归属和有效成员关系读取独立 AI 聊天。
func (q *LoadInboxQuery) agentConversationDetailsQuery(workspaceID, identityID, userID sqlArg) *bun.SelectQuery {
	return withAgentConversationDetails(q.agentConversationAccessQuery(workspaceID, identityID), identityID, userID)
}

// withAgentConversationDetails 为 AI 会话阅读基线追加消息摘要及当前运行状态。
func withAgentConversationDetails(query *bun.SelectQuery, identityID, userID sqlArg) *bun.SelectQuery {
	return withIndividualConversationDetails(query, identityID, userID).
		ColumnExpr("cv.title, oi.id AS agent_identity_id, oi.display_name AS agent_name, oi.avatar_file_id AS agent_avatar_file_id, agent.status AS agent_status, latest_agent_run.status AS agent_run_status").
		ColumnExpr("? = ANY(agent.service_audiences) AS agent_personal, agent.paused_at IS NOT NULL AS agent_paused, agent_computer.revoked_at IS NOT NULL AS agent_computer_revoked", domain.ServiceAudiencePersonal).
		ColumnExpr(servermodels.ComputerOnlineExpr("agent_computer")+" AS agent_computer_online").
		ColumnExpr(`EXISTS (
			SELECT 1 FROM service_conversations AS open_svc
			JOIN service_sessions AS open_ss ON open_ss.workspace_id = open_svc.workspace_id AND open_ss.id = open_svc.current_service_session_id
			WHERE open_svc.workspace_id = cv.workspace_id AND open_svc.conversation_id = cv.id AND open_ss.status = ?
		) AS service_open`, domain.ServiceSessionStatusOpen).
		Join("LEFT JOIN computers AS agent_computer ON agent_computer.workspace_id = agent.workspace_id AND agent_computer.id = agent.computer_id").
		Join("LEFT JOIN LATERAL (SELECT agr.status FROM agent_runs AS agr WHERE agr.workspace_id = cv.workspace_id AND agr.conversation_id = cv.id AND agr.agent_identity_id = ac.agent_identity_id ORDER BY agr.created_at DESC, agr.id DESC LIMIT 1) AS latest_agent_run ON TRUE")
}

// groupConversationsQuery 共用群聊成员范围、个人状态和未读统计。
func (q *LoadInboxQuery) groupConversationsQuery(workspaceID, identityID, userID sqlArg) *bun.SelectQuery {
	return q.groupConversationAccessQuery(workspaceID, identityID).
		ColumnExpr("COALESCE(cv.title, '') AS title").
		ColumnExpr(messagequery.GroupMemberPreviewNamesExpr+" AS member_preview_names", identityID, messagequery.GroupMemberPreviewNamesLimit).
		ColumnExpr("cv.image_file_id::text AS image_file_id").
		ColumnExpr("cv.status AS status").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("msg.type AS last_message_type, msg.system_event_type AS last_system_event_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_message_id::text AS last_message_id").
		ColumnExpr("members.member_count AS member_count").
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("unread.mentioned_unread_count AS mentioned_unread_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("COALESCE(state.marked_unread, false) AS marked_unread").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("state.archived_at").
		Join("JOIN LATERAL (SELECT count(*) AS member_count FROM conversation_participants AS member_cp WHERE member_cp.workspace_id = cv.workspace_id AND member_cp.conversation_id = cv.id AND member_cp.left_at IS NULL) AS members ON TRUE").
		Join("LEFT JOIN messages AS msg ON msg.workspace_id = cv.workspace_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.workspace_id = msg.workspace_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, identityID))
}
