//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ReportConversationTypingAction 按成员的会话发送资格向该会话的接收方发布输入状态。
type ReportConversationTypingAction struct {
	db *bun.DB
}

// NewReportConversationTypingAction 创建会话输入状态上报 Action。
func NewReportConversationTypingAction(db *bun.DB) *ReportConversationTypingAction {
	return &ReportConversationTypingAction{db: db}
}

// Execute 按成员在会话上的发送资格向接收方发布输入状态，不写数据库也不推进会话版本；无资格或会话不提供输入状态时返回 ErrConversationNotFound。
func (a *ReportConversationTypingAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string, active bool) error {
	send, err := conversationaccess.CheckSendable(ctx, a.db, identity, conversationID)
	if err != nil {
		return fmt.Errorf("authorize conversation typing: %w", err)
	}
	if !send.Allowed() {
		return ErrConversationNotFound
	}
	workspaceID := identity.Workspace.ID
	switch {
	// 单聊、群聊与普通 AI 员工会话的参与者向其余在职真人参与者发布。
	case (send.Role == conversationaccess.SendRoleMember || send.Role == conversationaccess.SendRoleAgentOwner && !send.Service) && send.SubjectID != nil:
		return a.publishToMembers(ctx, identity, conversationID, *send.SubjectID, active)
	// 网站渠道客户会话向访客发布。
	case send.Role == conversationaccess.SendRoleServiceReply && send.ChannelType != nil && *send.ChannelType == domain.ChannelTypeWebsite && send.ChannelIdentityID != nil:
		realtime.Publish(realtime.VisitorDirectoryTyping(workspaceID, *send.ChannelIdentityID, conversationID, active))
		return nil
	// 成员发起的 AI 员工服务会话向发起成员发布。
	case send.Role == conversationaccess.SendRoleServiceReply && send.Type == domain.ConversationTypeAgent && send.RequesterUserID != nil && send.SubjectID != nil:
		realtime.Publish(realtime.UserConversationTyping(workspaceID, *send.RequesterUserID, conversationID, *send.SubjectID, active))
		return nil
	// 其他渠道、副驾驶线程与服务周期中的发起人不提供输入状态。
	default:
		return ErrConversationNotFound
	}
}

// publishToMembers 向会话中除本人外的在职真人参与者发布输入状态。
func (a *ReportConversationTypingAction) publishToMembers(ctx context.Context, identity *servermodels.Identity, conversationID, senderSubjectID string, active bool) error {
	var userIDs []string
	if err := a.db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN users AS u ON u.workspace_id = cs.workspace_id AND u.identity_id = cs.source_id").
		Column("u.id").
		Where("cp.workspace_id = ? AND cp.conversation_id = ? AND cp.left_at IS NULL", identity.Workspace.ID, conversationID).
		Where("u.id <> ? AND u.status = ?", identity.User.ID, domain.IdentityStatusActive).
		Scan(ctx, &userIDs); err != nil {
		return fmt.Errorf("load conversation typing audience: %w", err)
	}
	realtime.Publish(arr.Map(userIDs, func(userID string) realtime.Notification {
		return realtime.UserConversationTyping(identity.Workspace.ID, userID, conversationID, senderSubjectID, active)
	})...)
	return nil
}
