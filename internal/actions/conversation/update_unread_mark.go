//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := chatstate.LockMember(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		state := ownState(tx, identity, conversationID)
		// 进入会话时清除已有个人状态中的未读标记。
		if !markedUnread {
			return state.update(ctx, "marked_unread", "marked_unread = false")
		}
		return state.upsert(ctx, &servermodels.ConversationUserState{MarkedUnread: true, Version: 1}, []string{"marked_unread"},
			"NOT cus.marked_unread", "marked_unread = true")
	})
	if err != nil {
		return fmt.Errorf("update conversation unread mark: %w", err)
	}
	return nil
}
