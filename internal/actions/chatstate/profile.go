//go:build server

package chatstate

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	"github.com/uptrace/bun"
)

// TouchIdentityConversations 推进展示指定企业身份名称、头像或账号状态的会话版本，网站访客页面展示成员与 AI 员工的名称和头像，同时通知访客目录受众：含已退出的参与记录、当前负责的客户会话、以其为 Agent 或创建人的 Copilot 线程所属客户会话，以及其运行记录和待执行队列所在的会话；调用方在完成资料对象的全部写入后调用。
func TouchIdentityConversations(ctx context.Context, db bun.IDB, organizationID, identityID string) error {
	participants := db.NewSelect().TableExpr("conversation_participants AS cp").
		Column("cp.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
		Where("cp.organization_id = ?", organizationID).
		Where("cs.kind = ? AND cs.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identityID)
	assigned := db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS ss ON ss.organization_id = cc.organization_id AND ss.id = svc.current_service_session_id").
		Where("cc.organization_id = ? AND ss.assignee_identity_id = ?", organizationID, identityID)
	copilotThreads := db.NewSelect().TableExpr("service_copilot_threads AS sct").
		ColumnExpr("sct.served_conversation_id").
		Where("sct.organization_id = ?", organizationID).
		Where("sct.agent_identity_id = ? OR sct.created_by_identity_id = ?", identityID, identityID)
	runs := db.NewSelect().TableExpr("agent_runs AS agr").
		Column("agr.conversation_id").
		Where("agr.organization_id = ? AND agr.agent_identity_id = ?", organizationID, identityID)
	lanes := db.NewSelect().TableExpr("agent_lanes AS al").
		ColumnExpr("al.scope_id").
		Where("al.organization_id = ? AND al.agent_identity_id = ? AND al.scope_kind = ?", organizationID, identityID, domain.AgentExecutionScopeConversation)
	return touchProfileConversations(ctx, db, organizationID, participants.Union(assigned).Union(copilotThreads).Union(runs).Union(lanes), true)
}

// TouchChannelIdentityConversations 推进指定客户渠道身份所在客户会话的版本，访客页面不展示客户资料，不通知访客目录受众；调用方在完成该渠道身份的全部写入后调用。
func TouchChannelIdentityConversations(ctx context.Context, db bun.IDB, organizationID, channelIdentityID string) error {
	return touchProfileConversations(ctx, db, organizationID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Where("cc.organization_id = ? AND cc.contact_channel_identity_id = ?", organizationID, channelIdentityID), false)
}

// TouchContactProfileConversations 推进联系人全部客户会话的版本，客服侧栏与 Copilot 据此重读客户档案；访客页面不展示客户档案，不通知访客目录受众；调用方在完成该联系人档案的全部写入后调用。
func TouchContactProfileConversations(ctx context.Context, db bun.IDB, organizationID, contactID string) error {
	return TouchContactsProfileConversations(ctx, db, organizationID, db.NewSelect().ColumnExpr("?::uuid", contactID))
}

// TouchContactsProfileConversations 推进子查询给出的联系人全部客户会话的版本，用于字段或标签定义变化影响多个联系人的档案；不通知访客目录受众。
func TouchContactsProfileConversations(ctx context.Context, db bun.IDB, organizationID string, contactIDs *bun.SelectQuery) error {
	return touchProfileConversations(ctx, db, organizationID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Where("cc.organization_id = ? AND cci.contact_id IN (?)", organizationID, contactIDs), false)
}

// TouchChannelConversations 推进指定渠道下全部客户会话的版本，访客页面不展示渠道名称，不通知访客目录受众；调用方在完成该渠道的全部写入后调用。
func TouchChannelConversations(ctx context.Context, db bun.IDB, organizationID, channelID string) error {
	return touchProfileConversations(ctx, db, organizationID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Where("cc.organization_id = ? AND cci.channel_id = ?", organizationID, channelID), false)
}

// TouchTeamConversations 推进当前客服周期属于指定团队的会话版本，并通知企业客服受众。
func TouchTeamConversations(ctx context.Context, db bun.IDB, organizationID, teamID string) error {
	return touchProfileConversations(ctx, db, organizationID, db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS ss ON ss.organization_id = cc.organization_id AND ss.id = svc.current_service_session_id").
		Where("cc.organization_id = ? AND ss.team_id = ?", organizationID, teamID), false)
}

// NotifyDirectPeersWorkStatusChanged 以参与方变化通知单聊对端重读会话摘要，用于只在单聊展示的工作状态变化；不推进会话版本。
func NotifyDirectPeersWorkStatusChanged(ctx context.Context, db bun.IDB, organizationID, identityID string) error {
	var rows []struct {
		ConversationID string `bun:"conversation_id"`
		Version        int64  `bun:"version"`
		PeerUserID     string `bun:"peer_user_id"`
	}
	if err := db.NewSelect().TableExpr("direct_conversations AS dc").
		ColumnExpr("cv.id AS conversation_id, cv.version, peer_u.id AS peer_user_id").
		Join("JOIN conversations AS cv ON cv.organization_id = dc.organization_id AND cv.id = dc.conversation_id").
		Join("JOIN organization_identities AS peer_oi ON peer_oi.organization_id = dc.organization_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END", identityID).
		Join("JOIN users AS peer_u ON peer_u.organization_id = peer_oi.organization_id AND peer_u.identity_id = peer_oi.id AND peer_u.status = ?", domain.IdentityStatusActive).
		Where("dc.organization_id = ? AND ? IN (dc.first_identity_id, dc.second_identity_id)", organizationID, identityID).
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("load direct peers for work status: %w", err)
	}
	for _, row := range rows {
		realtime.Notify(ctx, realtime.UserConversationChanged(organizationID, row.PeerUserID, row.ConversationID, domain.ConversationTypeDirect, row.Version, domain.ConversationChangeParticipants))
	}
	return nil
}

// touchProfileConversations 以参与方变化推进资料展示所在会话的版本；notifyVisitor 为真时同时登记网站访客目录受众。
func touchProfileConversations(ctx context.Context, db bun.IDB, organizationID string, conversationIDs *bun.SelectQuery, notifyVisitor bool) error {
	_, err := touchConversations(ctx, db, organizationID, conversationIDs, domain.ConversationChangeParticipants, notifyVisitor)
	return err
}
