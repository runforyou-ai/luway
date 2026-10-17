//go:build server

package groupchat

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// LeaveGroupConversationAction 退出普通成员参与的群聊。
type LeaveGroupConversationAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	canceller GroupAgentRunCanceller
}

// DissolveGroupConversationAction 解散群聊并保留当前成员的只读历史。
type DissolveGroupConversationAction struct {
	db        *bun.DB
	canceller GroupAgentRunCanceller
}

// NewLeaveGroupConversationAction 创建群聊退出操作。
func NewLeaveGroupConversationAction(db *bun.DB, enqueuer servertask.TxEnqueuer, canceller GroupAgentRunCanceller) *LeaveGroupConversationAction {
	return &LeaveGroupConversationAction{db: db, enqueuer: enqueuer, canceller: canceller}
}

// NewDissolveGroupConversationAction 创建群聊解散操作。
func NewDissolveGroupConversationAction(db *bun.DB, canceller GroupAgentRunCanceller) *DissolveGroupConversationAction {
	return &DissolveGroupConversationAction{db: db, canceller: canceller}
}

// Execute 退出群聊并带走本人负责的个人 AI 员工，取消等待本人确认与带走的个人 AI 员工提交的操作；群主必须先通过转让操作成为普通成员。
func (a *LeaveGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) error {
	conversationID, _ = str.NormalizeUUID(conversationID)
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		if group.Role == string(domain.ConversationParticipantRoleOwner) {
			return &conversationaction.ConflictError{Reason: ConflictReasonGroupOwnerCannotLeave}
		}
		if err := leaveGroupParticipant(ctx, tx, identity.Workspace.ID, group.ParticipantID); err != nil {
			return err
		}
		if err := agentprocess.CancelConversationParticipantToolDecisions(ctx, tx, a.enqueuer, identity.Workspace.ID, conversationID, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.UserConversationRemoved(identity.Workspace.ID, identity.User.ID, conversationID))
		if _, err := conversationaction.ClearConversationPin(ctx, tx, identity.Workspace.ID, identity.User.ID, conversationID); err != nil {
			return err
		}
		if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberLeft, Actor: groupActorSnapshot(identity),
		}); err != nil {
			return err
		}
		removed, err := removeGroupPersonalAgents(ctx, tx, a.enqueuer, a.canceller, identity.Workspace.ID, conversationID, identity.User.ID)
		if err != nil || len(removed) == 0 {
			return err
		}
		_, err = appendGroupSystemEvent(ctx, tx, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberRemoved, Actor: groupActorSnapshot(identity), Targets: removed,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("leave group conversation: %w", err)
	}
	return nil
}

// Execute 在群聊锁内幂等解散群聊并保留成员关系。
func (a *DissolveGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (GroupConversation, error) {
	conversationID, _ = str.NormalizeUUID(conversationID)
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 已解散群校验群主身份后返回幂等结果。
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupReadable)
		if err != nil {
			return err
		}
		if group.Role != string(domain.ConversationParticipantRoleOwner) {
			return chatstate.ErrGroupOwnerRequired
		}
		if group.Conversation.Status == string(domain.ConversationStatusActive) {
			if err := a.canceller.CancelForGroupConversation(ctx, tx, identity.Workspace.ID, conversationID); err != nil {
				return err
			}
			if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
				Type: domain.ConversationSystemEventGroupDissolved, Actor: groupActorSnapshot(identity),
			}); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model(group.Conversation).
				Set("status = ?", domain.ConversationStatusArchived).WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		result, err = loadGroupConversation(ctx, tx, identity, conversationID)
		return err
	})
	if err != nil {
		return GroupConversation{}, fmt.Errorf("dissolve group conversation: %w", err)
	}
	return result, nil
}
