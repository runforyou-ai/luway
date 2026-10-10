//go:build server

package conversationaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// SendRole 是成员在会话中发送消息所用的身份。
type SendRole string

const (
	// SendRoleMember 是单聊或群聊的参与者。
	SendRoleMember SendRole = "member"
	// SendRoleAgentOwner 是 AI 员工会话的所属成员。
	SendRoleAgentOwner SendRole = "agent_owner"
	// SendRoleCopilot 是在副驾驶线程中提问的成员。
	SendRoleCopilot SendRole = "copilot"
	// SendRoleServiceReply 是回复服务会话发起人的成员。
	SendRoleServiceReply SendRole = "service_reply"
)

// Denial 是成员不能在会话中发送消息的原因。
type Denial string

const (
	// DenialNone 表示可以发送。
	DenialNone Denial = ""
	// DenialNotFound 表示会话不存在或成员没有发送身份。
	DenialNotFound Denial = "not_found"
	// DenialAgentUnavailable 表示副驾驶线程的 AI 员工已停用。
	DenialAgentUnavailable Denial = "agent_unavailable"
	// DenialPersonalAgentUnbound 表示个人 AI 员工的电脑已撤销。
	DenialPersonalAgentUnbound Denial = "personal_agent_unbound"
	// DenialPersonalAgentPaused 表示个人 AI 员工已暂停。
	DenialPersonalAgentPaused Denial = "personal_agent_paused"
	// DenialServiceHandlingRequired 表示成员未开启处理服务请求。
	DenialServiceHandlingRequired Denial = "service_handling_required"
	// DenialServiceOwnRequest 表示成员是服务会话的发起人。
	DenialServiceOwnRequest Denial = "service_own_request"
	// DenialChannelOutboundUnsupported 表示来源渠道不支持外发。
	DenialChannelOutboundUnsupported Denial = "channel_outbound_unsupported"
	// DenialChannelRecipientUnbound 表示渠道身份已不再绑定会话发起人。
	DenialChannelRecipientUnbound Denial = "channel_recipient_unbound"
	// DenialChannelReplyWindowClosed 表示渠道回复窗口不可用。
	DenialChannelReplyWindowClosed Denial = "channel_reply_window_closed"
	// DenialChannelOutboundUnavailable 表示渠道已停用或未连接可发送的平台账号。
	DenialChannelOutboundUnavailable Denial = "channel_outbound_unavailable"
	// DenialServiceNotReplyable 表示当前服务周期已关闭。
	DenialServiceNotReplyable Denial = "service_not_replyable"
	// DenialServiceOwned 表示当前服务周期由其他身份负责。
	DenialServiceOwned Denial = "service_owned"
)

// Send 是成员在会话上的发送资格：发送身份、不可发送的原因与回复受众。
type Send struct {
	Type   domain.ConversationType
	Role   SendRole
	Denial Denial
	// Service 表示会话承载服务会话。
	Service bool
	// SubjectID 是成员的聊天主体编号，尚未建立时为空。
	SubjectID *string
	// ChannelType 与 ChannelIdentityID 是渠道来源服务会话的渠道与发起人渠道身份。
	ChannelType       *domain.ChannelType
	ChannelIdentityID *string
	// RequesterUserID 是由成员发起的服务会话的发起成员账号。
	RequesterUserID *string
}

// Allowed 判断成员可以发送。
func (s Send) Allowed() bool {
	return s.Denial == DenialNone
}

// sendFacts 是判定发送资格所需的会话、成员、服务周期与渠道事实。
type sendFacts struct {
	Type               domain.ConversationType      `bun:"type"`
	Status             domain.ConversationStatus    `bun:"status"`
	SubjectID          *string                      `bun:"subject_id"`
	Participant        bool                         `bun:"participant"`
	HandlesRequests    bool                         `bun:"handles_requests"`
	Service            bool                         `bun:"service"`
	Source             *domain.ServiceSource        `bun:"source"`
	Requester          bool                         `bun:"requester"`
	RequesterUserID    *string                      `bun:"requester_user_id"`
	SessionStatus      *domain.ServiceSessionStatus `bun:"session_status"`
	AssigneeID         *string                      `bun:"assignee_identity_id"`
	DirectPeerActive   bool                         `bun:"direct_peer_active"`
	AgentOwner         bool                         `bun:"agent_owner"`
	AgentParticipant   bool                         `bun:"agent_participant"`
	AgentActive        bool                         `bun:"agent_active"`
	AgentPaused        bool                         `bun:"agent_paused"`
	AgentUnbound       bool                         `bun:"agent_unbound"`
	CopilotServed      bool                         `bun:"copilot_served"`
	CopilotAgentActive bool                         `bun:"copilot_agent_active"`
	ChannelIdentityID  *string                      `bun:"channel_identity_id"`
	ChannelType        *domain.ChannelType          `bun:"channel_type"`
	ChannelEnabled     bool                         `bun:"channel_enabled"`
	ProviderBound      bool                         `bun:"provider_bound"`
	RecipientBound     bool                         `bun:"recipient_bound"`
	ReplyWindowOpen    bool                         `bun:"reply_window_open"`
}

// CheckSendable 以一次查询读取成员在会话上的发送资格；写入路径须在持有会话锁后另行复核。
func CheckSendable(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) (Send, error) {
	viewer := ViewerOf(identity)
	identityID := identity.WorkspaceIdentity.ID
	var facts sendFacts
	err := db.NewSelect().TableExpr("conversations AS cv").
		ColumnExpr("cv.type, cv.status, mine_cs.id::text AS subject_id").
		ColumnExpr("? AS participant", participant(viewer, "cv", false)).
		ColumnExpr("COALESCE(mine_oi.handles_service_requests, FALSE) AS handles_requests").
		ColumnExpr("svc.id IS NOT NULL AS service, svc.source").
		ColumnExpr("COALESCE(requester_cs.kind = ? AND requester_cs.source_id = ?, FALSE) AS requester", domain.ChatSubjectKindWorkspaceIdentity, identityID).
		ColumnExpr("requester_u.id::text AS requester_user_id").
		ColumnExpr("ss.status AS session_status, ss.assignee_identity_id::text AS assignee_identity_id").
		// 单聊对方仍是有效真人成员。
		ColumnExpr(`EXISTS (
			SELECT 1 FROM direct_conversations AS dc
			JOIN workspace_identities AS peer_oi ON peer_oi.workspace_id = dc.workspace_id AND peer_oi.id = CASE WHEN dc.first_identity_id = ? THEN dc.second_identity_id ELSE dc.first_identity_id END
			JOIN users AS peer_u ON peer_u.workspace_id = peer_oi.workspace_id AND peer_u.identity_id = peer_oi.id
			WHERE dc.workspace_id = cv.workspace_id AND dc.conversation_id = cv.id AND ? IN (dc.first_identity_id, dc.second_identity_id)
				AND peer_oi.type = ? AND peer_u.status = ?) AS direct_peer_active`, identityID, identityID, domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive).
		ColumnExpr("COALESCE(ac.user_identity_id = ?, FALSE) AS agent_owner", identityID).
		// AI 员工仍是会话参与者。
		ColumnExpr(`EXISTS (
			SELECT 1 FROM conversation_participants AS agent_cp
			JOIN chat_subjects AS agent_cs ON agent_cs.workspace_id = agent_cp.workspace_id AND agent_cs.id = agent_cp.subject_id
			WHERE agent_cp.workspace_id = ac.workspace_id AND agent_cp.conversation_id = ac.conversation_id AND agent_cp.left_at IS NULL
				AND agent_cs.kind = ? AND agent_cs.source_id = ac.agent_identity_id) AS agent_participant`, domain.ChatSubjectKindWorkspaceIdentity).
		ColumnExpr("COALESCE(agent.status = ?, FALSE) AS agent_active", domain.IdentityStatusActive).
		ColumnExpr("agent.paused_at IS NOT NULL AS agent_paused, agent_computer.revoked_at IS NOT NULL AS agent_unbound").
		ColumnExpr("served.id IS NOT NULL AS copilot_served").
		ColumnExpr("COALESCE(copilot_agent.status = ?, FALSE) AS copilot_agent_active", domain.IdentityStatusActive).
		ColumnExpr("ci.id::text AS channel_identity_id, ch.type AS channel_type, COALESCE(ch.enabled, FALSE) AS channel_enabled, ch.provider_account_id IS NOT NULL AS provider_bound").
		ColumnExpr("COALESCE(?, FALSE) AS recipient_bound", bun.Safe("("+deliveryaction.RecipientBoundCondition+")")).
		ColumnExpr(`EXISTS (
			SELECT 1 FROM channel_reply_windows AS crw
			WHERE crw.workspace_id = ci.workspace_id AND crw.channel_identity_id = ci.id AND ?) AS reply_window_open`, deliveryaction.UsableReplyWindowCondition("crw", 1)).
		Join("LEFT JOIN chat_subjects AS mine_cs ON mine_cs.workspace_id = cv.workspace_id AND mine_cs.kind = ? AND mine_cs.source_id = ?", domain.ChatSubjectKindWorkspaceIdentity, identityID).
		Join("LEFT JOIN workspace_identities AS mine_oi ON mine_oi.workspace_id = cv.workspace_id AND mine_oi.id = ?", identityID).
		Join("LEFT JOIN service_conversations AS svc ON svc.workspace_id = cv.workspace_id AND svc.conversation_id = cv.id").
		Join("LEFT JOIN service_sessions AS ss ON ss.workspace_id = svc.workspace_id AND ss.id = svc.current_service_session_id").
		Join("LEFT JOIN chat_subjects AS requester_cs ON requester_cs.workspace_id = svc.workspace_id AND requester_cs.id = svc.requester_subject_id").
		Join("LEFT JOIN users AS requester_u ON requester_u.workspace_id = requester_cs.workspace_id AND requester_u.identity_id = requester_cs.source_id AND requester_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN agent_conversations AS ac ON ac.workspace_id = cv.workspace_id AND ac.conversation_id = cv.id").
		Join("LEFT JOIN agents AS agent ON agent.workspace_id = ac.workspace_id AND agent.identity_id = ac.agent_identity_id").
		Join("LEFT JOIN computers AS agent_computer ON agent_computer.workspace_id = agent.workspace_id AND agent_computer.id = agent.computer_id").
		Join("LEFT JOIN service_copilot_threads AS sct ON sct.workspace_id = cv.workspace_id AND sct.conversation_id = cv.id").
		Join("LEFT JOIN service_conversations AS served ON served.workspace_id = sct.workspace_id AND served.conversation_id = sct.served_conversation_id").
		Join("LEFT JOIN agents AS copilot_agent ON copilot_agent.workspace_id = sct.workspace_id AND copilot_agent.identity_id = sct.agent_identity_id").
		Join("LEFT JOIN channel_conversations AS cc ON cc.workspace_id = svc.workspace_id AND cc.conversation_id = svc.conversation_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
		Join("LEFT JOIN channels AS ch ON ch.workspace_id = ci.workspace_id AND ch.id = ci.channel_id").
		Where("cv.workspace_id = ? AND cv.id = ?", identity.Workspace.ID, conversationID).
		Scan(ctx, &facts)
	if errors.Is(err, sql.ErrNoRows) {
		return Send{Denial: DenialNotFound}, nil
	}
	if err != nil {
		return Send{}, fmt.Errorf("load conversation send facts: %w", err)
	}
	return decideSend(facts, identityID), nil
}

// decideSend 按会话类别确定成员的发送身份与不可发送的原因：服务会话中除 AI 员工会话的所属成员外均按回复发起人判定。
func decideSend(f sendFacts, identityID string) Send {
	send := Send{Type: f.Type, Service: f.Service, SubjectID: f.SubjectID, ChannelType: f.ChannelType, ChannelIdentityID: f.ChannelIdentityID, RequesterUserID: f.RequesterUserID}
	active := f.Status == domain.ConversationStatusActive
	switch {
	case f.Service && !(f.Type == domain.ConversationTypeAgent && f.AgentOwner):
		send.Role = SendRoleServiceReply
		send.Denial = serviceReplyDenial(f, identityID)
	case f.Type == domain.ConversationTypeCopilot:
		send.Role = SendRoleCopilot
		switch {
		case !f.CopilotServed || !active:
			send.Denial = DenialNotFound
		case !f.CopilotAgentActive:
			send.Denial = DenialAgentUnavailable
		}
	case f.Type == domain.ConversationTypeDirect:
		send.Role = SendRoleMember
		if !f.Participant || !active || !f.DirectPeerActive {
			send.Denial = DenialNotFound
		}
	case f.Type == domain.ConversationTypeGroup:
		send.Role = SendRoleMember
		if !f.Participant || !active {
			send.Denial = DenialNotFound
		}
	case f.Type == domain.ConversationTypeAgent:
		send.Role = SendRoleAgentOwner
		serviceOpen := f.SessionStatus != nil && *f.SessionStatus == domain.ServiceSessionStatusOpen
		switch {
		// AI 员工停用后只能继续进行中的服务周期。
		case !f.AgentOwner || !f.Participant || !f.AgentParticipant || !active || (!f.AgentActive && !serviceOpen):
			send.Denial = DenialNotFound
		// 电脑已撤销优先于暂停。
		case f.AgentUnbound:
			send.Denial = DenialPersonalAgentUnbound
		case f.AgentPaused:
			send.Denial = DenialPersonalAgentPaused
		}
	default:
		send.Denial = DenialNotFound
	}
	return send
}

// serviceReplyDenial 按成员服务会话回复的校验顺序给出不可回复发起人的原因：接待资格、发起人本人、渠道外发能力与回复窗口、周期开放、周期负责人。
func serviceReplyDenial(f sendFacts, identityID string) Denial {
	if !f.HandlesRequests {
		return DenialServiceHandlingRequired
	}
	if f.Requester {
		return DenialServiceOwnRequest
	}
	if f.Source != nil && *f.Source == domain.ServiceSourceChannel {
		if f.ChannelType == nil {
			return DenialNotFound
		}
		capabilities := domain.ChannelCapabilitiesOf(*f.ChannelType)
		if !capabilities.Outbound() {
			return DenialChannelOutboundUnsupported
		}
		if capabilities.ViaPlatform() {
			windowOpen := !capabilities.ReplyWindow || f.ReplyWindowOpen
			switch {
			case !f.RecipientBound:
				return DenialChannelRecipientUnbound
			case !windowOpen:
				return DenialChannelReplyWindowClosed
			case !f.ChannelEnabled || !f.ProviderBound:
				return DenialChannelOutboundUnavailable
			}
		}
	}
	if f.SessionStatus == nil {
		return DenialNotFound
	}
	if *f.SessionStatus == domain.ServiceSessionStatusClosed {
		return DenialServiceNotReplyable
	}
	if f.AssigneeID != nil && *f.AssigneeID != identityID {
		return DenialServiceOwned
	}
	return DenialNone
}
