//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
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
	if !common.ValidUUID(conversationID) {
		return &ValidationError{Fields: map[string]ValidationCode{"conversationId": ValidationConversationIDInvalid}}
	}
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
		organizationID, userID := identity.Organization.ID, identity.User.ID
		if !archived {
			if err := notifyConversationStateWrite(ctx, tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
				Set("archived_at = NULL").Set("version = version + 1").Set("updated_at = now()").
				Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND archived_at IS NOT NULL", organizationID, conversationID, userID).
				Returning("version"), organizationID, conversationID, userID); err != nil {
				return fmt.Errorf("clear conversation archive: %w", err)
			}
			return nil
		}
		unpinned, err := ClearConversationPin(ctx, tx, organizationID, userID, conversationID)
		if err != nil {
			return err
		}
		if unpinned {
			if _, err := advancePinOrderVersion(ctx, tx, organizationID, userID); err != nil {
				return err
			}
		}
		// 个人状态行不存在时先以版本 0 建立；已归档的聊天保持原归档时间。
		if _, err := tx.NewInsert().Model(&servermodels.ConversationUserState{
			OrganizationID: organizationID, ConversationID: conversationID, UserID: userID,
		}).Column("organization_id", "conversation_id", "user_id", "version").
			On("CONFLICT (organization_id, conversation_id, user_id) DO NOTHING").Exec(ctx); err != nil {
			return fmt.Errorf("create conversation user state: %w", err)
		}
		if err := notifyConversationStateWrite(ctx, tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("archived_at = now()").Set("version = version + 1").Set("updated_at = now()").
			Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND archived_at IS NULL", organizationID, conversationID, userID).
			Returning("version"), organizationID, conversationID, userID); err != nil {
			return fmt.Errorf("save conversation archive: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update conversation archive: %w", err)
	}
	return nil
}
