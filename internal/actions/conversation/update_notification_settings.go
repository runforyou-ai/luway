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

// UpdateConversationNotificationSettingsAction 保存当前用户的会话提醒设置。
type UpdateConversationNotificationSettingsAction struct{ db *bun.DB }

// NewUpdateConversationNotificationSettingsAction 创建会话提醒设置操作。
func NewUpdateConversationNotificationSettingsAction(db *bun.DB) *UpdateConversationNotificationSettingsAction {
	return &UpdateConversationNotificationSettingsAction{db: db}
}

// Execute 校验原生会话访问权并保存静音状态。
func (a *UpdateConversationNotificationSettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string, muted bool) (ConversationNotificationSettings, error) {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
		if err != nil {
			return err
		}
		if member.Conversation.Type != string(domain.ConversationTypeGroup) && member.Conversation.Status != string(domain.ConversationStatusActive) {
			return ErrConversationNotFound
		}
		row := &servermodels.ConversationUserState{Muted: muted}
		// 首次插入时仅静音计为一次个人状态变化。
		if muted {
			row.Version = 1
		}
		return ownState(tx, identity, conversationID).upsert(ctx, row, []string{"muted"}, "cus.muted <> EXCLUDED.muted", "muted = EXCLUDED.muted")
	})
	if err != nil {
		return ConversationNotificationSettings{}, fmt.Errorf("update conversation notification settings: %w", err)
	}
	return ConversationNotificationSettings{Muted: muted}, nil
}
