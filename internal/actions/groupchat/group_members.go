//go:build server

package groupchat

import (
	"context"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AddGroupConversationMembersAction 批量增加群聊成员。
type AddGroupConversationMembersAction struct{ db *bun.DB }

// RemoveGroupConversationMemberAction 移除单个群聊成员。
type RemoveGroupConversationMemberAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	canceller GroupAgentRunCanceller
}

// NewAddGroupConversationMembersAction 创建群聊增员操作。
func NewAddGroupConversationMembersAction(db *bun.DB) *AddGroupConversationMembersAction {
	return &AddGroupConversationMembersAction{db: db}
}

// NewRemoveGroupConversationMemberAction 创建群聊成员移除操作。
func NewRemoveGroupConversationMemberAction(db *bun.DB, enqueuer servertask.TxEnqueuer, canceller GroupAgentRunCanceller) *RemoveGroupConversationMemberAction {
	return &RemoveGroupConversationMemberAction{db: db, enqueuer: enqueuer, canceller: canceller}
}

// Execute 增加有效企业成员，重新加入时复用原参与者行；群主可以加入任何有效成员，其他成员只能加入本人负责的个人 AI 员工。
func (a *AddGroupConversationMembersAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationMembersInput) (GroupConversation, error) {
	conversationID, memberIDs, fields := normalizeGroupMembersInput(identity.WorkspaceIdentity.ID, input.ConversationID, input.MemberIdentityIDs)
	if len(fields) > 0 {
		return GroupConversation{}, &common.FieldError{Fields: fields}
	}
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		members, err := loadActiveGroupMembers(ctx, tx, identity, memberIDs)
		if err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		if group.Role != string(domain.ConversationParticipantRoleOwner) {
			for _, member := range members {
				if !member.Personal {
					return chatstate.ErrGroupOwnerRequired
				}
			}
		}
		activeIDs, err := loadActiveGroupParticipantIdentityIDs(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		activeSet := set.Collect(activeIDs)
		for _, identityID := range memberIDs {
			if activeSet.Has(identityID) {
				return &conversationaction.ConflictError{Reason: ConflictReasonGroupMemberAlreadyActive}
			}
		}
		if len(activeIDs)+len(memberIDs) > maxGroupParticipantCount {
			return &common.FieldError{Fields: map[string]common.FieldCode{"memberIdentityIds": ValidationGroupMembersTooMany}}
		}

		subjects, err := chatstate.EnsureSubjects(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, memberIDs)
		if err != nil {
			return err
		}
		targets := make([]conversationaction.ConversationSystemEventParticipant, 0, len(members))
		participants := make([]*servermodels.ConversationParticipant, 0, len(members))
		for _, member := range members {
			participants = append(participants, &servermodels.ConversationParticipant{
				ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ConversationID: conversationID,
				SubjectID: subjects[member.IdentityID].ID, Role: string(domain.ConversationParticipantRoleMember),
			})
			targets = append(targets, conversationaction.ConversationSystemEventParticipant{IdentityID: member.IdentityID, DisplayName: member.DisplayName, PersonalResponsibleName: member.PersonalResponsibleName})
		}
		// 新成员创建参与者行，曾退出的成员复用原参与者行重新加入。
		if _, err := tx.NewInsert().Model(&participants).
			Column("id", "workspace_id", "conversation_id", "subject_id", "role").
			On("CONFLICT (workspace_id, conversation_id, subject_id) DO UPDATE").
			Set("left_at = NULL").
			Set("role = EXCLUDED.role").
			Exec(ctx); err != nil {
			return fmt.Errorf("add group conversation participants: %w", err)
		}
		eventMessage, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMembersAdded, Actor: groupActorSnapshot(identity), Targets: targets,
		})
		if err != nil {
			return err
		}
		// 新成员以本轮加入事件为已读基线。
		if _, err := tx.ExecContext(ctx, `
				INSERT INTO conversation_user_states (workspace_id, conversation_id, user_id, last_read_message_id, last_read_at, last_reviewed_mention_message_id, read_seq, version)
				SELECT u.workspace_id, cv.id, u.id, ?::uuid, now(), ?::uuid, ?, 1
				FROM users AS u
				JOIN conversations AS cv ON cv.workspace_id = u.workspace_id AND cv.id = ?
				WHERE u.workspace_id = ? AND u.identity_id IN (?)
				ON CONFLICT (workspace_id, conversation_id, user_id) DO UPDATE
				SET read_seq = EXCLUDED.read_seq, last_read_message_id = EXCLUDED.last_read_message_id, last_read_at = now(), last_reviewed_mention_message_id = EXCLUDED.last_reviewed_mention_message_id, version = conversation_user_states.version + 1
			`, eventMessage.ID, eventMessage.ID, eventMessage.MessageSeq, conversationID, identity.Workspace.ID, bun.List(memberIDs)); err != nil {
			return fmt.Errorf("initialize added group member read states: %w", err)
		}
		// 按本轮入群基线清理已覆盖的查看记录。
		if _, err := tx.NewDelete().Model((*servermodels.ConversationMentionReview)(nil)).
			Where("workspace_id = ? AND conversation_id = ?", identity.Workspace.ID, conversationID).
			Where("user_id IN (SELECT id FROM users WHERE workspace_id = ? AND identity_id IN (?))", identity.Workspace.ID, bun.List(memberIDs)).Exec(ctx); err != nil {
			return fmt.Errorf("reset added group member mention reviews: %w", err)
		}
		result, err = loadGroupConversation(ctx, tx, identity, conversationID)
		return err
	})
	if err != nil {
		return GroupConversation{}, fmt.Errorf("add group conversation members: %w", err)
	}
	return result, nil
}

// Execute 将当前有效的普通成员移出群聊，群主可以移出任何成员，个人 AI 员工的负责人可以移出本人负责的个人 AI 员工；被移出的真人负责的个人 AI 员工随之移出，等待被移出成员确认与被移出 AI 员工提交的操作取消。
func (a *RemoveGroupConversationMemberAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationMemberInput) (GroupConversation, error) {
	conversationID, _ := str.NormalizeUUID(input.ConversationID)
	memberID, _ := str.NormalizeUUID(input.MemberIdentityID)
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		target, err := loadActiveGroupParticipant(ctx, tx, identity.Workspace.ID, conversationID, memberID, false)
		if err != nil {
			return err
		}
		if group.Role != string(domain.ConversationParticipantRoleOwner) && target.PersonalResponsibleUserID != identity.User.ID {
			return chatstate.ErrGroupOwnerRequired
		}
		if target.Role == string(domain.ConversationParticipantRoleOwner) {
			return &conversationaction.ConflictError{Reason: ConflictReasonGroupOwnerCannotBeRemoved}
		}
		if err := leaveGroupParticipant(ctx, tx, identity.Workspace.ID, target.ParticipantID); err != nil {
			return err
		}
		if err := agentprocess.CancelConversationParticipantToolDecisions(ctx, tx, a.enqueuer, identity.Workspace.ID, conversationID, target.IdentityID); err != nil {
			return err
		}
		// 被移出的真人成员收到会话失权通知，并清除该会话的个人置顶。
		removedUserIDs := make([]string, 0, 1)
		if err := tx.NewSelect().Table("users").Column("id").
			Where("workspace_id = ? AND identity_id = ?", identity.Workspace.ID, target.IdentityID).
			Scan(ctx, &removedUserIDs); err != nil {
			return fmt.Errorf("load removed group member user: %w", err)
		}
		for _, userID := range removedUserIDs {
			realtime.Notify(ctx, realtime.UserConversationRemoved(identity.Workspace.ID, userID, conversationID))
			if _, err := conversationaction.ClearConversationPin(ctx, tx, identity.Workspace.ID, userID, conversationID); err != nil {
				return err
			}
		}
		if err := a.canceller.CancelForGroupAgent(ctx, tx, identity.Workspace.ID, conversationID, memberID); err != nil {
			return err
		}
		targets := []conversationaction.ConversationSystemEventParticipant{groupParticipantSnapshot(target)}
		for _, userID := range removedUserIDs {
			removed, err := removeGroupPersonalAgents(ctx, tx, a.enqueuer, a.canceller, identity.Workspace.ID, conversationID, userID)
			if err != nil {
				return err
			}
			targets = append(targets, removed...)
		}
		if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberRemoved, Actor: groupActorSnapshot(identity), Targets: targets,
		}); err != nil {
			return err
		}
		result, err = loadGroupConversation(ctx, tx, identity, conversationID)
		return err
	})
	if err != nil {
		return GroupConversation{}, fmt.Errorf("remove group conversation member: %w", err)
	}
	return result, nil
}

// normalizeGroupMembersInput 规范化群聊批量增员参数并按顺序去重，新增成员不得包含当前成员。
func normalizeGroupMembersInput(currentIdentityID, conversationID string, memberIDs []string) (string, []string, map[string]common.FieldCode) {
	fields := map[string]common.FieldCode{}
	conversationID, _ = str.NormalizeUUID(conversationID)
	var seen set.Set[string]
	normalizedIDs := make([]string, 0, len(memberIDs))
	for _, identityID := range memberIDs {
		identityID, _ = str.NormalizeUUID(identityID)
		if identityID == currentIdentityID {
			fields["memberIdentityIds"] = ValidationGroupMemberIDsInvalid
			continue
		}
		if seen.Add(identityID) {
			normalizedIDs = append(normalizedIDs, identityID)
		}
	}
	return conversationID, normalizedIDs, fields
}
