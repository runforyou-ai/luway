//go:build server

package groupchat

import (
	"context"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// UpdateGroupConversationAction 修改群聊资料。
type UpdateGroupConversationAction struct{ db *bun.DB }

// NewUpdateGroupConversationAction 创建群聊资料修改操作。
func NewUpdateGroupConversationAction(db *bun.DB) *UpdateGroupConversationAction {
	return &UpdateGroupConversationAction{db: db}
}

// Execute 修改群聊资料，并在名称变化时记录系统事件。
func (a *UpdateGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationProfileInput) (GroupConversation, error) {
	normalized := normalizeGroupProfileInput(input)
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, normalized.ConversationID, chatstate.GroupManageable)
		if err != nil {
			return err
		}
		title := support.Deref(group.Conversation.Title)
		description := support.Deref(group.Conversation.Description)
		nextImageFileID := group.Conversation.ImageFileID
		imageChanged := false
		if normalized.ImageFileID != nil {
			nextImageFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeGroupImage, *normalized.ImageFileID, group.Conversation.ImageFileID)
			if err != nil {
				return err
			}
			imageChanged = group.Conversation.ImageFileID == nil || *group.Conversation.ImageFileID != *normalized.ImageFileID
		}
		if title != normalized.Title || description != normalized.Description || imageChanged {
			if err := tx.NewUpdate().Model(group.Conversation).
				Set("title = ?", support.NilIfZero(normalized.Title)).
				Set("description = ?", support.NilIfZero(normalized.Description)).
				Set("image_file_id = ?", nextImageFileID).
				Set("version = version + 1").
				WherePK().Where("workspace_id = ?", identity.Workspace.ID).
				Returning("version").Scan(ctx); err != nil {
				return fmt.Errorf("update group conversation profile: %w", err)
			}
			if err := chatstate.NotifyConversationChanged(ctx, tx, group.Conversation, domain.ConversationChangeParticipants); err != nil {
				return err
			}
			if imageChanged {
				if err := fileaction.RetireLinkedImage(ctx, tx, identity.Workspace.ID, group.Conversation.ImageFileID, nextImageFileID); err != nil {
					return err
				}
			}
		}
		if title != normalized.Title {
			previousTitle := title
			eventTitle := normalized.Title
			if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
				Type:          domain.ConversationSystemEventGroupRenamed,
				Actor:         groupActorSnapshot(identity),
				PreviousTitle: &previousTitle,
				Title:         &eventTitle,
			}); err != nil {
				return err
			}
		}
		result, err = loadGroupConversation(ctx, tx, identity, normalized.ConversationID)
		return err
	})
	if err != nil {
		return GroupConversation{}, fmt.Errorf("update group conversation: %w", err)
	}
	return result, nil
}

// normalizeGroupProfileInput 规范化群聊资料修改参数。
func normalizeGroupProfileInput(input GroupConversationProfileInput) GroupConversationProfileInput {
	input.ConversationID, _ = str.NormalizeUUID(input.ConversationID)
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.ImageFileID != nil {
		imageFileID, _ := str.NormalizeUUID(*input.ImageFileID)
		input.ImageFileID = &imageFileID
	}
	return input
}
