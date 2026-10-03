//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateGroupConversationAction 修改群聊资料。
type UpdateGroupConversationAction struct{ db *bun.DB }

// AddGroupConversationMembersAction 批量增加群聊成员。
type AddGroupConversationMembersAction struct{ db *bun.DB }

// RemoveGroupConversationMemberAction 移除单个群聊成员。
type RemoveGroupConversationMemberAction struct {
	db          *bun.DB
	coordinator GroupAgentRunCoordinator
}

// TransferGroupConversationOwnerAction 转让群主。
type TransferGroupConversationOwnerAction struct{ db *bun.DB }

// LeaveGroupConversationAction 退出普通成员参与的群聊。
type LeaveGroupConversationAction struct {
	db          *bun.DB
	coordinator GroupAgentRunCoordinator
}

// DissolveGroupConversationAction 解散群聊并保留当前成员的只读历史。
type DissolveGroupConversationAction struct {
	db          *bun.DB
	coordinator GroupAgentRunCoordinator
}

// GroupAgentRunCoordinator 在群成员变化事务内收敛受影响 AI 员工的执行。
type GroupAgentRunCoordinator interface {
	CancelForGroupAgent(context.Context, bun.IDB, string, string, string) error
	CancelForGroupConversation(context.Context, bun.IDB, string, string) error
}

type activeGroupParticipantRow struct {
	ParticipantID             string  `bun:"participant_id"`
	IdentityID                string  `bun:"identity_id"`
	DisplayName               string  `bun:"display_name"`
	Role                      string  `bun:"role"`
	PersonalResponsibleUserID string  `bun:"personal_responsible_user_id"`
	PersonalResponsibleName   *string `bun:"personal_responsible_name"`
}

// NewUpdateGroupConversationAction 创建群聊资料修改操作。
func NewUpdateGroupConversationAction(db *bun.DB) *UpdateGroupConversationAction {
	return &UpdateGroupConversationAction{db: db}
}

// NewAddGroupConversationMembersAction 创建群聊增员操作。
func NewAddGroupConversationMembersAction(db *bun.DB) *AddGroupConversationMembersAction {
	return &AddGroupConversationMembersAction{db: db}
}

// NewRemoveGroupConversationMemberAction 创建群聊成员移除操作。
func NewRemoveGroupConversationMemberAction(db *bun.DB, coordinator GroupAgentRunCoordinator) *RemoveGroupConversationMemberAction {
	return &RemoveGroupConversationMemberAction{db: db, coordinator: coordinator}
}

// NewTransferGroupConversationOwnerAction 创建群主转让操作。
func NewTransferGroupConversationOwnerAction(db *bun.DB) *TransferGroupConversationOwnerAction {
	return &TransferGroupConversationOwnerAction{db: db}
}

// NewLeaveGroupConversationAction 创建群聊退出操作。
func NewLeaveGroupConversationAction(db *bun.DB, coordinator GroupAgentRunCoordinator) *LeaveGroupConversationAction {
	return &LeaveGroupConversationAction{db: db, coordinator: coordinator}
}

// NewDissolveGroupConversationAction 创建群聊解散操作。
func NewDissolveGroupConversationAction(db *bun.DB, coordinator GroupAgentRunCoordinator) *DissolveGroupConversationAction {
	return &DissolveGroupConversationAction{db: db, coordinator: coordinator}
}

// Execute 修改群聊资料，并在名称变化时记录系统事件。
func (a *UpdateGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationProfileInput) (GroupConversation, error) {
	normalized, fields := normalizeGroupProfileInput(input)
	if len(fields) > 0 {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, normalized.ConversationID, chatstate.GroupManageable)
		if err != nil {
			return err
		}
		title := common.StringValue(group.Conversation.Title)
		description := common.StringValue(group.Conversation.Description)
		nextImageFileID := group.Conversation.ImageFileID
		imageChanged := false
		if normalized.ImageFileID != nil {
			nextImageFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeGroupImage, *normalized.ImageFileID, group.Conversation.ImageFileID)
			if err != nil {
				return err
			}
			imageChanged = group.Conversation.ImageFileID == nil || *group.Conversation.ImageFileID != *normalized.ImageFileID
		}
		if title != normalized.Title || description != normalized.Description || imageChanged {
			if err := tx.NewUpdate().Model(group.Conversation).
				Set("title = ?", common.OptionalString(normalized.Title)).
				Set("description = ?", common.OptionalString(normalized.Description)).
				Set("image_file_id = ?", nextImageFileID).
				Set("version = version + 1").
				Set("updated_at = now()").
				WherePK().Where("organization_id = ?", identity.Organization.ID).
				Returning("version").Scan(ctx); err != nil {
				return fmt.Errorf("update group conversation profile: %w", err)
			}
			if err := chatstate.NotifyConversationChanged(ctx, tx, group.Conversation, domain.ConversationChangeParticipants); err != nil {
				return err
			}
			if imageChanged {
				if err := fileaction.RetireLinkedImage(ctx, tx, identity.Organization.ID, group.Conversation.ImageFileID, nextImageFileID); err != nil {
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

// Execute 增加有效企业成员，重新加入时复用原参与者行；群主可以加入任何有效成员，其他成员只能加入本人负责的个人 AI 员工。
func (a *AddGroupConversationMembersAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationMembersInput) (GroupConversation, error) {
	conversationID, memberIDs, fields := normalizeGroupMembersInput(identity.OrganizationIdentity.ID, input.ConversationID, input.MemberIdentityIDs)
	if len(fields) > 0 {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: fields}
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
		activeIDs, err := loadActiveGroupParticipantIdentityIDs(ctx, tx, identity.Organization.ID, conversationID)
		if err != nil {
			return err
		}
		activeSet := make(map[string]struct{}, len(activeIDs))
		for _, identityID := range activeIDs {
			activeSet[identityID] = struct{}{}
		}
		for _, identityID := range memberIDs {
			if _, exists := activeSet[identityID]; exists {
				return &conversationaction.ConflictError{Reason: ConflictReasonGroupMemberAlreadyActive}
			}
		}
		if len(activeIDs)+len(memberIDs) > maxGroupParticipantCount {
			return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"memberIdentityIds": ValidationGroupMembersTooMany}}
		}

		subjects, err := conversationaction.EnsureOrganizationIdentityChatSubjects(ctx, tx, identity.Organization.ID, memberIDs)
		if err != nil {
			return err
		}
		targets := make([]conversationaction.ConversationSystemEventParticipant, 0, len(members))
		participants := make([]*servermodels.ConversationParticipant, 0, len(members))
		for _, member := range members {
			participants = append(participants, &servermodels.ConversationParticipant{
				ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, ConversationID: conversationID,
				SubjectID: subjects[member.IdentityID].ID, Role: string(domain.ConversationParticipantRoleMember),
			})
			targets = append(targets, conversationaction.ConversationSystemEventParticipant{IdentityID: member.IdentityID, DisplayName: member.DisplayName, PersonalResponsibleName: member.PersonalResponsibleName})
		}
		// 新成员创建参与者行，曾退出的成员复用原参与者行重新加入。
		if _, err := tx.NewInsert().Model(&participants).
			Column("id", "organization_id", "conversation_id", "subject_id", "role").
			On("CONFLICT (organization_id, conversation_id, subject_id) DO UPDATE").
			Set("left_at = NULL").
			Set("role = EXCLUDED.role").
			Set("updated_at = now()").
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
				INSERT INTO conversation_user_states (organization_id, conversation_id, user_id, last_read_message_id, last_read_at, last_reviewed_mention_message_id, read_seq, version)
				SELECT u.organization_id, cv.id, u.id, ?::uuid, now(), ?::uuid, ?, 1
				FROM users AS u
				JOIN conversations AS cv ON cv.organization_id = u.organization_id AND cv.id = ?
				WHERE u.organization_id = ? AND u.identity_id IN (?)
				ON CONFLICT (organization_id, conversation_id, user_id) DO UPDATE
				SET read_seq = EXCLUDED.read_seq, last_read_message_id = EXCLUDED.last_read_message_id, last_read_at = now(), last_reviewed_mention_message_id = EXCLUDED.last_reviewed_mention_message_id, version = conversation_user_states.version + 1, updated_at = now()
			`, eventMessage.ID, eventMessage.ID, eventMessage.MessageSeq, conversationID, identity.Organization.ID, bun.In(memberIDs)); err != nil {
			return fmt.Errorf("initialize added group member read states: %w", err)
		}
		// 按本轮入群基线清理已覆盖的查看记录。
		if _, err := tx.NewDelete().Model((*servermodels.ConversationMentionReview)(nil)).
			Where("organization_id = ? AND conversation_id = ?", identity.Organization.ID, conversationID).
			Where("user_id IN (SELECT id FROM users WHERE organization_id = ? AND identity_id IN (?))", identity.Organization.ID, bun.In(memberIDs)).Exec(ctx); err != nil {
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

// Execute 将当前有效的普通成员移出群聊，群主可以移出任何成员，个人 AI 员工的负责人可以移出本人负责的个人 AI 员工；被移出的真人负责的个人 AI 员工随之移出。
func (a *RemoveGroupConversationMemberAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationMemberInput) (GroupConversation, error) {
	conversationID, memberID, fields := normalizeGroupMemberInput(input.ConversationID, input.MemberIdentityID, "memberIdentityId", ValidationGroupMemberIDInvalid)
	if len(fields) > 0 {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		target, err := loadActiveGroupParticipant(ctx, tx, identity.Organization.ID, conversationID, memberID, false)
		if err != nil {
			return err
		}
		if group.Role != string(domain.ConversationParticipantRoleOwner) && target.PersonalResponsibleUserID != identity.User.ID {
			return chatstate.ErrGroupOwnerRequired
		}
		if target.Role == string(domain.ConversationParticipantRoleOwner) {
			return &conversationaction.ConflictError{Reason: ConflictReasonGroupOwnerCannotBeRemoved}
		}
		if err := leaveGroupParticipant(ctx, tx, identity.Organization.ID, target.ParticipantID); err != nil {
			return err
		}
		// 被移出的真人成员收到会话失权通知，并清除该会话的个人置顶。
		removedUserIDs := make([]string, 0, 1)
		if err := tx.NewSelect().Table("users").Column("id").
			Where("organization_id = ? AND identity_id = ?", identity.Organization.ID, target.IdentityID).
			Scan(ctx, &removedUserIDs); err != nil {
			return fmt.Errorf("load removed group member user: %w", err)
		}
		for _, userID := range removedUserIDs {
			realtime.Notify(ctx, realtime.UserConversationRemoved(identity.Organization.ID, userID, conversationID))
			if _, err := conversationaction.ClearConversationPin(ctx, tx, identity.Organization.ID, userID, conversationID); err != nil {
				return err
			}
		}
		if err := a.coordinator.CancelForGroupAgent(ctx, tx, identity.Organization.ID, conversationID, memberID); err != nil {
			return err
		}
		targets := []conversationaction.ConversationSystemEventParticipant{groupParticipantSnapshot(target)}
		for _, userID := range removedUserIDs {
			removed, err := removeGroupPersonalAgents(ctx, tx, a.coordinator, identity.Organization.ID, conversationID, userID)
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

// Execute 将群主角色转让给另一位当前成员。
func (a *TransferGroupConversationOwnerAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationOwnerInput) (GroupConversation, error) {
	conversationID, ownerID, fields := normalizeGroupMemberInput(input.ConversationID, input.OwnerIdentityID, "ownerIdentityId", ValidationGroupOwnerIDInvalid)
	if len(fields) > 0 {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result GroupConversation
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		group, err := chatstate.LockGroup(ctx, tx, identity, conversationID, chatstate.GroupManageable)
		if err != nil {
			return err
		}
		if ownerID == identity.OrganizationIdentity.ID {
			result, err = loadGroupConversation(ctx, tx, identity, conversationID)
			return err
		}
		target, err := loadActiveGroupParticipant(ctx, tx, identity.Organization.ID, conversationID, ownerID, true)
		if err != nil {
			return err
		}
		if err := transferGroupOwner(ctx, tx, identity.Organization.ID, group.ParticipantID, target.ParticipantID); err != nil {
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

// Execute 退出群聊并带走本人负责的个人 AI 员工，群主必须先通过转让操作成为普通成员。
func (a *LeaveGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) error {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"conversationId": conversationaction.ValidationConversationIDInvalid}}
	}
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
		if err := leaveGroupParticipant(ctx, tx, identity.Organization.ID, group.ParticipantID); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.UserConversationRemoved(identity.Organization.ID, identity.User.ID, conversationID))
		if _, err := conversationaction.ClearConversationPin(ctx, tx, identity.Organization.ID, identity.User.ID, conversationID); err != nil {
			return err
		}
		if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
			Type: domain.ConversationSystemEventGroupMemberLeft, Actor: groupActorSnapshot(identity),
		}); err != nil {
			return err
		}
		removed, err := removeGroupPersonalAgents(ctx, tx, a.coordinator, identity.Organization.ID, conversationID, identity.User.ID)
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
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"conversationId": conversationaction.ValidationConversationIDInvalid}}
	}
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
			if err := a.coordinator.CancelForGroupConversation(ctx, tx, identity.Organization.ID, conversationID); err != nil {
				return err
			}
			if _, err := createGroupSystemEvent(ctx, tx, identity, group.Conversation, conversationaction.ConversationSystemEvent{
				Type: domain.ConversationSystemEventGroupDissolved, Actor: groupActorSnapshot(identity),
			}); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model(group.Conversation).
				Set("status = ?", domain.ConversationStatusArchived).
				Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
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

// normalizeGroupProfileInput 规范化群聊资料修改参数。
func normalizeGroupProfileInput(input GroupConversationProfileInput) (GroupConversationProfileInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	normalizedConversationID, valid := common.NormalizeUUID(input.ConversationID)
	if !valid {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	input.ConversationID = normalizedConversationID
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(input.Title) > maxGroupTitleLength {
		fields["title"] = ValidationGroupTitleTooLong
	}
	if utf8.RuneCountInString(input.Description) > maxGroupDescriptionLength {
		fields["description"] = ValidationGroupDescriptionTooLong
	}
	if input.ImageFileID != nil {
		imageFileID, imageFileIDValid := common.NormalizeUUID(strings.TrimSpace(*input.ImageFileID))
		if !imageFileIDValid {
			fields["imageFileId"] = ValidationGroupImageFileIDInvalid
		}
		input.ImageFileID = &imageFileID
	}
	return input, fields
}

// normalizeGroupMembersInput 规范化群聊批量增员参数。
func normalizeGroupMembersInput(currentIdentityID, conversationID string, memberIDs []string) (string, []string, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	normalizedConversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	if len(memberIDs) == 0 {
		fields["memberIdentityIds"] = ValidationGroupMembersRequired
	} else if len(memberIDs) > maxGroupParticipantCount-1 {
		fields["memberIdentityIds"] = ValidationGroupMembersTooMany
	}
	seen := make(map[string]struct{}, len(memberIDs))
	normalizedIDs := make([]string, 0, len(memberIDs))
	for _, identityID := range memberIDs {
		identityID, valid = common.NormalizeUUID(identityID)
		if !valid || identityID == currentIdentityID {
			fields["memberIdentityIds"] = ValidationGroupMemberIDsInvalid
			continue
		}
		if _, exists := seen[identityID]; exists {
			fields["memberIdentityIds"] = ValidationGroupMemberIDsInvalid
			continue
		}
		seen[identityID] = struct{}{}
		normalizedIDs = append(normalizedIDs, identityID)
	}
	return normalizedConversationID, normalizedIDs, fields
}

// normalizeGroupMemberInput 规范化群聊单成员操作参数。
func normalizeGroupMemberInput(conversationID, identityID, field string, code conversationaction.ValidationCode) (string, string, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	normalizedConversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	normalizedIdentityID, valid := common.NormalizeUUID(identityID)
	if !valid {
		fields[field] = code
	}
	return normalizedConversationID, normalizedIdentityID, fields
}

// loadActiveGroupParticipantIdentityIDs 读取群聊当前有效成员编号。
func loadActiveGroupParticipantIdentityIDs(ctx context.Context, db bun.IDB, organizationID, conversationID string) ([]string, error) {
	identityIDs := make([]string, 0)
	if err := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cs.source_id").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Where("cp.organization_id = ?", organizationID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		OrderExpr("cs.source_id ASC").
		Scan(ctx, &identityIDs); err != nil {
		return nil, fmt.Errorf("load active group participant identity ids: %w", err)
	}
	return identityIDs, nil
}

// loadActiveGroupParticipant 锁定指定的当前有效群成员。
func loadActiveGroupParticipant(ctx context.Context, db bun.IDB, organizationID, conversationID, identityID string, requireActiveUser bool) (activeGroupParticipantRow, error) {
	row := activeGroupParticipantRow{}
	query := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id AS participant_id").
		ColumnExpr("cs.source_id AS identity_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("cp.role AS role").
		ColumnExpr("CASE WHEN ? = ANY(a.service_audiences) THEN a.responsible_user_id::text ELSE '' END AS personal_responsible_user_id", domain.ServiceAudiencePersonal).
		ColumnExpr("? AS personal_responsible_name", conversationaction.PersonalResponsibleName("oi")).
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ? AND cs.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identityID).
		Join("JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id").
		Join("LEFT JOIN agents AS a ON a.organization_id = oi.organization_id AND a.identity_id = oi.id")
	if requireActiveUser {
		query = query.Join("JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id AND u.status = ?", domain.IdentityStatusActive)
	}
	err := query.
		Where("cp.organization_id = ?", organizationID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		For("UPDATE OF cp").
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return activeGroupParticipantRow{}, &conversationaction.ConflictError{Reason: ConflictReasonGroupMemberNotActive}
	}
	if err != nil {
		return activeGroupParticipantRow{}, fmt.Errorf("load active group participant: %w", err)
	}
	return row, nil
}

// transferGroupOwner 在已锁定群聊中原子切换唯一群主。
func transferGroupOwner(ctx context.Context, db bun.IDB, organizationID, currentParticipantID, successorParticipantID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("role = CASE WHEN id = ? THEN ? ELSE ? END", successorParticipantID, domain.ConversationParticipantRoleOwner, domain.ConversationParticipantRoleMember).
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
		Where("id IN (?)", bun.In([]string{currentParticipantID, successorParticipantID})).
		Exec(ctx); err != nil {
		return fmt.Errorf("transfer group conversation owner: %w", err)
	}
	return nil
}

// leaveGroupParticipant 标记成员已经退出群聊。
func leaveGroupParticipant(ctx context.Context, db bun.IDB, organizationID, participantID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("left_at = now()").
		Set("role = ?", domain.ConversationParticipantRoleMember).
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
		Where("id = ?", participantID).
		Where("left_at IS NULL").
		Exec(ctx); err != nil {
		return fmt.Errorf("leave group conversation participant: %w", err)
	}
	return nil
}

// createGroupSystemEvent 写入类型化系统事件并推进会话摘要与操作人的已读位置。
func createGroupSystemEvent(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversation *servermodels.Conversation, event conversationaction.ConversationSystemEvent) (*servermodels.Message, error) {
	message, err := appendGroupSystemEvent(ctx, db, conversation, event)
	if err != nil {
		return nil, err
	}
	state := &servermodels.ConversationUserState{
		OrganizationID: identity.Organization.ID, ConversationID: conversation.ID,
		UserID: identity.User.ID, LastReadMessageID: &message.ID,
	}
	if err := conversationaction.AdvanceConversationUserReadState(ctx, db, state, message); err != nil {
		return nil, err
	}
	return message, nil
}

// appendGroupSystemEvent 写入类型化系统事件并推进会话摘要。
func appendGroupSystemEvent(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, event conversationaction.ConversationSystemEvent) (*servermodels.Message, error) {
	if event.Targets == nil {
		event.Targets = make([]conversationaction.ConversationSystemEventParticipant, 0)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal group system event: %w", err)
	}
	eventType := string(event.Type)
	message := &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: conversation.OrganizationID, ConversationID: conversation.ID,
		Type: string(domain.MessageTypeSystem), Body: "", SystemEventType: &eventType, SystemEventPayload: payload,
		OriginatedAt: time.Now().UTC(),
	}
	message, _, err = chatstate.AppendMessage(ctx, db, conversation, message)
	return message, err
}

// groupActorSnapshot 记录操作人的审计快照。
func groupActorSnapshot(identity *servermodels.Identity) conversationaction.ConversationSystemEventParticipant {
	return conversationaction.ConversationSystemEventParticipant{IdentityID: identity.OrganizationIdentity.ID, DisplayName: identity.OrganizationIdentity.DisplayName}
}

// groupParticipantSnapshot 记录目标成员的审计快照。
func groupParticipantSnapshot(participant activeGroupParticipantRow) conversationaction.ConversationSystemEventParticipant {
	return conversationaction.ConversationSystemEventParticipant{IdentityID: participant.IdentityID, DisplayName: participant.DisplayName, PersonalResponsibleName: participant.PersonalResponsibleName}
}
