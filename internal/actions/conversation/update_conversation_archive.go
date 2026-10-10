//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateConversationArchiveAction 保存当前用户的会话归档状态。
type UpdateConversationArchiveAction struct{ db *bun.DB }

// NewUpdateConversationArchiveAction 创建个人归档写入操作。
func NewUpdateConversationArchiveAction(db *bun.DB) *UpdateConversationArchiveAction {
	return &UpdateConversationArchiveAction{db: db}
}

// Execute 校验群聊、单聊或 AI 聊天的成员资格后写入或清除归档时间；归档同时取消置顶，会话此后的新对话消息由消息写入清除归档。
func (a *UpdateConversationArchiveAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string, archived bool) error {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 会话锁与消息追加互斥，归档与新消息清除归档按提交顺序生效。
		member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
		if err != nil {
			return err
		}
		switch domain.ConversationType(member.Conversation.Type) {
		case domain.ConversationTypeGroup, domain.ConversationTypeDirect, domain.ConversationTypeAgent:
		default:
			return ErrConversationNotFound
		}
		state := ownState(tx, identity, conversationID)
		if !archived {
			return state.update(ctx, "archived_at IS NOT NULL", "archived_at = NULL")
		}
		unpinned, err := ClearConversationPin(ctx, tx, identity.Workspace.ID, identity.User.ID, conversationID)
		if err != nil {
			return err
		}
		if unpinned {
			if _, err := advancePinOrderVersion(ctx, tx, identity.Workspace.ID, identity.User.ID); err != nil {
				return err
			}
		}
		// 已归档的聊天保持原归档时间。
		if err := state.ensure(ctx); err != nil {
			return err
		}
		return state.update(ctx, "archived_at IS NULL", "archived_at = now()")
	})
	if err != nil {
		return fmt.Errorf("update conversation archive: %w", err)
	}
	return nil
}
