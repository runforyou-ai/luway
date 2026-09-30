//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateConversationUnreadMarkAction 保存独立于阅读水位的个人未读标记。
type UpdateConversationUnreadMarkAction struct{ db *bun.DB }

// NewUpdateConversationUnreadMarkAction 创建个人未读标记操作。
func NewUpdateConversationUnreadMarkAction(db *bun.DB) *UpdateConversationUnreadMarkAction {
	return &UpdateConversationUnreadMarkAction{db: db}
}

// Execute 校验内部会话成员资格，只更新当前用户的未读标记。
func (a *UpdateConversationUnreadMarkAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string, markedUnread bool) error {
	if !common.ValidUUID(conversationID) {
		return &ValidationError{Fields: map[string]ValidationCode{"conversationId": ValidationConversationIDInvalid}}
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := chatstate.LockMember(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		// 进入会话时清除已有个人状态中的未读标记。
		if !markedUnread {
			return notifyConversationStateWrite(ctx, tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
				Set("marked_unread = false").Set("version = version + 1").Set("updated_at = now()").
				Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND marked_unread", identity.Organization.ID, conversationID, identity.User.ID).
				Returning("version"), identity.Organization.ID, conversationID, identity.User.ID)
		}
		state := &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: conversationID,
			UserID: identity.User.ID, MarkedUnread: true, Version: 1,
		}
		if err := notifyConversationStateWrite(ctx, tx.NewInsert().Model(state).
			Column("organization_id", "conversation_id", "user_id", "marked_unread", "version").
			On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").
			Set("marked_unread = true").Set("version = cus.version + 1").Set("updated_at = now()").
			Where("NOT cus.marked_unread").Returning("version"), identity.Organization.ID, conversationID, identity.User.ID); err != nil {
			return fmt.Errorf("save conversation unread mark: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update conversation unread mark: %w", err)
	}
	return nil
}
