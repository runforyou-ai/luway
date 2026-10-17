//go:build server

package groupchat

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// TransferGroupConversationOwnerAction 转让群主。
type TransferGroupConversationOwnerAction struct{ db *bun.DB }

// NewTransferGroupConversationOwnerAction 创建群主转让操作。
func NewTransferGroupConversationOwnerAction(db *bun.DB) *TransferGroupConversationOwnerAction {
	return &TransferGroupConversationOwnerAction{db: db}
}

// Execute 将群主角色转让给另一位当前成员。
func (a *TransferGroupConversationOwnerAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationOwnerInput) (GroupConversation, error) {
	conversationID, _ := str.NormalizeUUID(input.ConversationID)
	ownerID, _ := str.NormalizeUUID(input.OwnerIdentityID)
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupManageable)
		if err != nil {
			return err
		}
		if ownerID == identity.WorkspaceIdentity.ID {
			result, err = loadGroupConversation(ctx, tx, identity, conversationID)
			return err
		}
		target, err := loadActiveGroupParticipant(ctx, tx, identity.Workspace.ID, conversationID, ownerID, true)
		if err != nil {
			return err
		}
		if err := transferGroupOwner(ctx, tx, identity.Workspace.ID, group.ParticipantID, target.ParticipantID); err != nil {
			return err
		}
		if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupOwnerTransferred, Actor: groupActorSnapshot(identity), Targets: []conversationaction.ConversationSystemEventParticipant{groupParticipantSnapshot(target)},
		}); err != nil {
			return err
		}
		result, err = loadGroupConversation(ctx, tx, identity, conversationID)
		return err
	})
	if err != nil {
		return GroupConversation{}, fmt.Errorf("transfer group conversation owner: %w", err)
	}
	return result, nil
}

// transferGroupOwner 在已锁定群聊中原子切换唯一群主。
func transferGroupOwner(ctx context.Context, db bun.IDB, workspaceID, currentParticipantID, successorParticipantID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("role = CASE WHEN id = ? THEN ? ELSE ? END", successorParticipantID, domain.ConversationParticipantRoleOwner, domain.ConversationParticipantRoleMember).
		Where("workspace_id = ?", workspaceID).
		Where("id IN (?)", bun.List([]string{currentParticipantID, successorParticipantID})).
		Exec(ctx); err != nil {
		return fmt.Errorf("transfer group conversation owner: %w", err)
	}
	return nil
}
