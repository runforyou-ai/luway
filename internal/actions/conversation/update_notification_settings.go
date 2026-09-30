//go:build server

package conversation

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	if !common.ValidUUID(conversationID) {
		return ConversationNotificationSettings{}, &ValidationError{Fields: map[string]ValidationCode{
			"conversationId": ValidationConversationIDInvalid,
		}}
	}
	result := ConversationNotificationSettings{Muted: muted}
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
		state := &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: conversationID,
			UserID: identity.User.ID, Muted: muted,
		}
		// 首次插入时仅静音计为一次个人状态变化。
		if muted {
			state.Version = 1
		}
		if err := notifyConversationStateWrite(ctx, tx.NewInsert().Model(state).
			Column("organization_id", "conversation_id", "user_id", "muted", "version").
			On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").
			Set("muted = EXCLUDED.muted").
			Set("version = cus.version + 1").
			Set("updated_at = now()").
			Where("cus.muted <> EXCLUDED.muted").
			Returning("version"), identity.Organization.ID, conversationID, identity.User.ID); err != nil {
			return fmt.Errorf("save conversation notification settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return ConversationNotificationSettings{}, fmt.Errorf("update conversation notification settings: %w", err)
	}
	return result, nil
}
