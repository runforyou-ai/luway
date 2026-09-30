//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ReportConversationTypingAction 校验成员的会话发送资格，并向该会话的接收方发布输入状态。
type ReportConversationTypingAction struct {
	db *bun.DB
}

// NewReportConversationTypingAction 创建会话输入状态上报 Action。
func NewReportConversationTypingAction(db *bun.DB) *ReportConversationTypingAction {
	return &ReportConversationTypingAction{db: db}
}

// Execute 按会话类型校验发送资格并发布输入状态，不写数据库也不推进会话版本；无资格时返回 ErrConversationNotFound。
func (a *ReportConversationTypingAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string, active bool) error {
	if !common.ValidUUID(conversationID) {
		return ErrConversationNotFound
	}
	var conversationType string
	err := a.db.NewSelect().Model((*servermodels.Conversation)(nil)).Column("cv.type").
		Where("cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).
		Scan(ctx, &conversationType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("load conversation type: %w", err)
	}
	switch domain.ConversationType(conversationType) {
	case domain.ConversationTypeDirect, domain.ConversationTypeGroup:
		return a.publishToMembers(ctx, identity, conversationID, active)
	case domain.ConversationTypeChannel:
		return a.publishToVisitor(ctx, identity, conversationID, active)
	case domain.ConversationTypeAgent:
		return a.publishToRequester(ctx, identity, conversationID, active)
	default:
		return ErrConversationNotFound
	}
}

// publishToRequester 要求 AI 聊天承载的服务周期开放且无人负责或由当前身份负责，向企业成员发起人发布输入状态。
func (a *ReportConversationTypingAction) publishToRequester(ctx context.Context, identity *servermodels.Identity, conversationID string, active bool) error {
	route := struct {
		RequesterUserID    string  `bun:"requester_user_id"`
		SenderSubjectID    string  `bun:"sender_subject_id"`
		SessionStatus      string  `bun:"session_status"`
		AssigneeIdentityID *string `bun:"assignee_identity_id"`
	}{}
	err := a.db.NewSelect().TableExpr("service_conversations AS svc").
		ColumnExpr("u.id AS requester_user_id, mine.id AS sender_subject_id, ss.status AS session_status, ss.assignee_identity_id").
		Join("JOIN service_sessions AS ss ON ss.organization_id = svc.organization_id AND ss.id = svc.current_service_session_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.organization_id = svc.organization_id AND requester_cs.id = svc.requester_subject_id AND requester_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN users AS u ON u.organization_id = requester_cs.organization_id AND u.identity_id = requester_cs.source_id").
		Join("JOIN chat_subjects AS mine ON mine.organization_id = svc.organization_id AND mine.kind = ? AND mine.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID).
		Where("svc.organization_id = ? AND svc.conversation_id = ?", identity.Organization.ID, conversationID).
		Where("u.id <> ?", identity.User.ID).
		Scan(ctx, &route)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize requester typing: %w", err)
	}
	// 回复发起人的输入状态要求周期开放，且周期无人负责或由本人负责。
	if domain.ServiceSessionStatus(route.SessionStatus) != domain.ServiceSessionStatusOpen ||
		(route.AssigneeIdentityID != nil && *route.AssigneeIdentityID != identity.OrganizationIdentity.ID) {
		return ErrConversationNotFound
	}
	realtime.Publish(realtime.UserConversationTyping(identity.Organization.ID, route.RequesterUserID, conversationID, route.SenderSubjectID, active))
	return nil
}

// publishToMembers 要求当前身份是活跃单聊或群聊的现有参与者，向其余在职真人成员发布输入状态。
func (a *ReportConversationTypingAction) publishToMembers(ctx context.Context, identity *servermodels.Identity, conversationID string, active bool) error {
	var senderSubjectID string
	err := chatstate.MemberQuery(a.db, identity, conversationID).
		ColumnExpr("mine.subject_id").
		Where("cv.status = ?", domain.ConversationStatusActive).
		Scan(ctx, &senderSubjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize conversation typing: %w", err)
	}
	var userIDs []string
	if err := a.db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN users AS u ON u.organization_id = cs.organization_id AND u.identity_id = cs.source_id").
		Column("u.id").
		Where("cp.organization_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", identity.Organization.ID, conversationID).
		Where("u.id <> ? AND u.status = ?", identity.User.ID, domain.IdentityStatusActive).
		Scan(ctx, &userIDs); err != nil {
		return fmt.Errorf("load conversation typing audience: %w", err)
	}
	notifications := make([]realtime.Notification, 0, len(userIDs))
	for _, userID := range userIDs {
		notifications = append(notifications, realtime.UserConversationTyping(identity.Organization.ID, userID, conversationID, senderSubjectID, active))
	}
	realtime.Publish(notifications...)
	return nil
}

// customerTypingRoute 是客户会话对客回复资格与访客受众。
type customerTypingRoute struct {
	ChannelIdentityID  string  `bun:"channel_identity_id"`
	SessionStatus      string  `bun:"session_status"`
	AssigneeIdentityID *string `bun:"assignee_identity_id"`
}

// publishToVisitor 要求当前身份可对客回复网站渠道客户会话，向该渠道身份的访客发布输入状态。
func (a *ReportConversationTypingAction) publishToVisitor(ctx context.Context, identity *servermodels.Identity, conversationID string, active bool) error {
	route := customerTypingRoute{}
	err := a.db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cci.id AS channel_identity_id, ss.status AS session_status, ss.assignee_identity_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS ss ON ss.organization_id = cc.organization_id AND ss.conversation_id = cc.conversation_id AND ss.id = svc.current_service_session_id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Join("JOIN channels AS c ON c.organization_id = cci.organization_id AND c.id = cci.channel_id AND c.type = ?", domain.ChannelTypeWebsite).
		Where("cc.organization_id = ? AND cc.conversation_id = ?", identity.Organization.ID, conversationID).
		Scan(ctx, &route)
	// 非网站渠道不提供输入状态，与会话不存在同样处理。
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize customer typing: %w", err)
	}
	// 对客输入状态要求周期开放，且周期无人负责或由本人负责。
	if domain.ServiceSessionStatus(route.SessionStatus) != domain.ServiceSessionStatusOpen {
		return ErrConversationNotFound
	}
	if route.AssigneeIdentityID != nil && *route.AssigneeIdentityID != identity.OrganizationIdentity.ID {
		return ErrConversationNotFound
	}
	realtime.Publish(realtime.VisitorDirectoryTyping(identity.Organization.ID, route.ChannelIdentityID, conversationID, active))
	return nil
}
