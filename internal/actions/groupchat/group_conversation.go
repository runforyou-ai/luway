//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	maxGroupTitleLength       = 100
	maxGroupDescriptionLength = 500
	maxGroupParticipantCount  = 100
)

// CreateGroupConversationAction 创建企业内部群聊和初始成员关系。
type CreateGroupConversationAction struct {
	db *bun.DB
}

// GetGroupConversationQuery 读取企业内部群聊资料和当前成员。
type GetGroupConversationQuery struct {
	db *bun.DB
}

// SendGroupTextMessageAction 持久化企业内部群聊文本消息。
type SendGroupTextMessageAction struct {
	db             *bun.DB
	agentScheduler conversationaction.GroupAgentMessageScheduler
}

type groupMemberRow struct {
	IdentityID         string                          `bun:"identity_id"`
	IdentityType       domain.OrganizationIdentityType `bun:"identity_type"`
	DisplayName        string                          `bun:"display_name"`
	AvatarFileID       *string                         `bun:"avatar_file_id"`
	AssistantOwnerName *string                         `bun:"assistant_owner_name"`
}

type groupParticipantRow struct {
	IdentityType             domain.OrganizationIdentityType `bun:"identity_type"`
	ChatSubjectID            string                          `bun:"chat_subject_id"`
	IdentityID               string                          `bun:"identity_id"`
	DisplayName              string                          `bun:"display_name"`
	AvatarFileID             *string                         `bun:"avatar_file_id"`
	Role                     string                          `bun:"role"`
	AssistantOwnerName       *string                         `bun:"assistant_owner_name"`
	AssistantOwnerIdentityID *string                         `bun:"assistant_owner_identity_id"`
}

// NewCreateGroupConversationAction 创建群聊创建操作。
func NewCreateGroupConversationAction(db *bun.DB) *CreateGroupConversationAction {
	return &CreateGroupConversationAction{db: db}
}

// NewGetGroupConversationQuery 创建群聊资料查询。
func NewGetGroupConversationQuery(db *bun.DB) *GetGroupConversationQuery {
	return &GetGroupConversationQuery{db: db}
}

// NewSendGroupTextMessageAction 创建群聊文本发送操作。
func NewSendGroupTextMessageAction(db *bun.DB, agentScheduler conversationaction.GroupAgentMessageScheduler) *SendGroupTextMessageAction {
	return &SendGroupTextMessageAction{db: db, agentScheduler: agentScheduler}
}

// Execute 创建包含有效企业成员的企业内部群聊。
func (a *CreateGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationInput) (GroupConversationSummary, error) {
	normalized, fields := normalizeGroupConversationInput(identity.OrganizationIdentity.ID, input)
	if len(fields) > 0 {
		return GroupConversationSummary{}, &conversationaction.ValidationError{Fields: fields}
	}

	conversationID := uuid.NewV7().String()
	participantIDs := make(map[string]string, len(normalized.MemberIdentityIDs)+1)
	for _, identityID := range append([]string{identity.OrganizationIdentity.ID}, normalized.MemberIdentityIDs...) {
		participantIDs[identityID] = uuid.NewV7().String()
	}

	var previewNames []string
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		members, err := loadActiveGroupMembers(ctx, tx, identity, normalized.MemberIdentityIDs)
		if err != nil {
			return err
		}
		// 初始成员同时入群，按名称顺序取前几名作为未命名群的显示成员。
		previewNames = make([]string, 0, chatstate.GroupMemberPreviewNamesLimit)
		for _, member := range members[:min(len(members), chatstate.GroupMemberPreviewNamesLimit)] {
			previewNames = append(previewNames, member.DisplayName)
		}
		var imageFileID *string
		if normalized.ImageFileID != "" {
			imageFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeGroupImage, normalized.ImageFileID, nil)
			if err != nil {
				return err
			}
		}
		subjects, err := conversationaction.EnsureOrganizationIdentityChatSubjects(ctx, tx, identity.Organization.ID, append([]string{identity.OrganizationIdentity.ID}, normalized.MemberIdentityIDs...))
		if err != nil {
			return err
		}
		creatorSubject := subjects[identity.OrganizationIdentity.ID]
		createdBySubjectID := creatorSubject.ID
		conversation := &servermodels.Conversation{
			ID: conversationID, OrganizationID: identity.Organization.ID,
			Type: string(domain.ConversationTypeGroup), Status: string(domain.ConversationStatusActive),
			Title: common.OptionalString(normalized.Title), Description: common.OptionalString(normalized.Description),
			ImageFileID: imageFileID, CreatedBySubjectID: &createdBySubjectID, Version: 1,
		}
		if _, err := tx.NewInsert().Model(conversation).
			Column("id", "organization_id", "type", "status", "title", "description", "image_file_id", "created_by_subject_id", "version").
			Exec(ctx); err != nil {
			return fmt.Errorf("create group conversation: %w", err)
		}

		participants := make([]*servermodels.ConversationParticipant, 0, len(members)+1)
		participants = append(participants, &servermodels.ConversationParticipant{
			ID: participantIDs[identity.OrganizationIdentity.ID], OrganizationID: identity.Organization.ID,
			ConversationID: conversation.ID, SubjectID: creatorSubject.ID,
			Role: string(domain.ConversationParticipantRoleOwner),
		})
		for _, member := range members {
			subject := subjects[member.IdentityID]
			participants = append(participants, &servermodels.ConversationParticipant{
				ID: participantIDs[member.IdentityID], OrganizationID: identity.Organization.ID,
				ConversationID: conversation.ID, SubjectID: subject.ID,
				Role: string(domain.ConversationParticipantRoleMember),
			})
		}
		if _, err := tx.NewInsert().Model(&participants).
			Column("id", "organization_id", "conversation_id", "subject_id", "role").
			Exec(ctx); err != nil {
			return fmt.Errorf("create group conversation participants: %w", err)
		}
		return chatstate.NotifyConversationChanged(ctx, tx, conversation, domain.ConversationChangeParticipants)
	})
	if err != nil {
		return GroupConversationSummary{}, fmt.Errorf("create group conversation: %w", err)
	}
	return GroupConversationSummary{
		ID: conversationID, Title: normalized.Title,
		Status: domain.ConversationStatusActive, MemberCount: len(normalized.MemberIdentityIDs) + 1,
		ImageFileID: common.OptionalString(normalized.ImageFileID), MemberPreviewNames: previewNames,
	}, nil
}

// Execute 委托共用查询返回当前成员可见的群聊。
func (q *GetGroupConversationQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (GroupConversation, error) {
	return loadGroupConversation(ctx, q.db, identity, conversationID)
}

// loadGroupConversation 返回当前成员可见的群聊资料和有效参与者。
func loadGroupConversation(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) (GroupConversation, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return GroupConversation{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{
			"conversationId": conversationaction.ValidationConversationIDInvalid,
		}}
	}
	var summary struct {
		Title              string     `bun:"title"`
		MemberPreviewNames []string   `bun:"member_preview_names,array"`
		Description        string     `bun:"description"`
		ImageFileID        *string    `bun:"image_file_id"`
		Status             string     `bun:"status"`
		CreatedAt          time.Time  `bun:"created_at"`
		Muted              bool       `bun:"muted"`
		ArchivedAt         *time.Time `bun:"archived_at"`
	}
	err := chatstate.GroupQuery(db, identity, conversationID).
		ColumnExpr("COALESCE(cv.title, '') AS title").
		ColumnExpr(chatstate.GroupMemberPreviewNamesExpr+" AS member_preview_names", identity.OrganizationIdentity.ID, chatstate.GroupMemberPreviewNamesLimit).
		ColumnExpr("COALESCE(cv.description, '') AS description").
		ColumnExpr("cv.image_file_id::text AS image_file_id").
		ColumnExpr("cv.status AS status").
		ColumnExpr("cv.created_at AS created_at").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("state.archived_at").
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
		Scan(ctx, &summary)
	if errors.Is(err, sql.ErrNoRows) {
		return GroupConversation{}, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return GroupConversation{}, fmt.Errorf("load group conversation: %w", err)
	}

	rows := make([]groupParticipantRow, 0)
	if err := db.NewSelect().
		TableExpr("conversation_participants AS cp").
		ColumnExpr("cs.id AS chat_subject_id").
		ColumnExpr("cs.source_id AS identity_id").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		ColumnExpr("cp.role AS role, oi.type AS identity_type").
		ColumnExpr("? AS assistant_owner_name", conversationaction.AssistantOwnerName("oi")).
		ColumnExpr("? AS assistant_owner_identity_id", assistantOwnerIdentityID("oi")).
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id").
		Where("cp.organization_id = ?", identity.Organization.ID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.left_at IS NULL").
		OrderExpr("CASE cp.role WHEN ? THEN 0 ELSE 1 END, lower(oi.display_name) ASC, oi.id ASC", domain.ConversationParticipantRoleOwner).
		Scan(ctx, &rows); err != nil {
		return GroupConversation{}, fmt.Errorf("list group conversation participants: %w", err)
	}
	participants := make([]GroupParticipant, 0, len(rows))
	for _, row := range rows {
		participants = append(participants, GroupParticipant{
			ChatSubjectID: row.ChatSubjectID, IdentityType: row.IdentityType,
			IdentityID: row.IdentityID, DisplayName: row.DisplayName,
			AvatarFileID: row.AvatarFileID, Role: domain.ConversationParticipantRole(row.Role),
			AssistantOwnerName: row.AssistantOwnerName, AssistantOwnerIdentityID: row.AssistantOwnerIdentityID,
		})
	}
	return GroupConversation{
		ID: conversationID, Title: summary.Title, Description: summary.Description, ImageFileID: summary.ImageFileID,
		Status:    domain.ConversationStatus(summary.Status),
		CreatedAt: summary.CreatedAt, Participants: participants,
		Muted: summary.Muted, ArchivedAt: summary.ArchivedAt, MemberPreviewNames: summary.MemberPreviewNames,
	}, nil
}

// Execute 在会话锁内幂等写入群聊文本消息。
func (a *SendGroupTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized, fields := normalizeGroupTextMessageInput(input)
	if len(fields) > 0 {
		return conversationaction.ConversationMessage{}, &conversationaction.ValidationError{Fields: fields}
	}
	messageID := uuid.NewV7()
	idempotencyKey := "mmsg:" + identity.OrganizationIdentity.ID + ":" + normalized.ClientMessageID
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		sendContext, err := chatstate.LockGroup(ctx, tx, identity, normalized.ConversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		if saved, found, err := loadIdempotentGroupMessage(ctx, tx, identity, normalized, idempotencyKey); err != nil || found {
			result = saved
			return err
		}
		reply, err := conversationaction.LoadConversationReplyTarget(ctx, tx, identity.Organization.ID, normalized.ConversationID, normalized.ReplyToMessageID)
		if err != nil {
			return err
		}
		mentions, err := loadGroupMentionTargets(ctx, tx, identity.Organization.ID, normalized.ConversationID, sendContext.SubjectID, normalized.MentionSubjectIDs)
		if err != nil {
			return err
		}
		var replyToMessageID *string
		if reply != nil {
			replyToMessageID = &reply.ID
		}
		message := &servermodels.Message{
			ID: messageID.String(), OrganizationID: identity.Organization.ID,
			ConversationID: normalized.ConversationID, SenderParticipantID: &sendContext.ParticipantID,
			Type: string(domain.MessageTypeText), Body: normalized.Body, ReplyToMessageID: replyToMessageID, MentionAll: normalized.MentionAll,
			ClientMessageID: &normalized.ClientMessageID, IdempotencyKey: &idempotencyKey, OriginatedAt: time.Now().UTC(),
		}
		message, inserted, err := chatstate.AppendMessage(ctx, tx, sendContext.Conversation, message)
		if err != nil {
			return err
		}
		if !inserted {
			result, _, err = loadIdempotentGroupMessage(ctx, tx, identity, normalized, idempotencyKey)
			return err
		}
		if err := conversationaction.CreateMessageMentions(ctx, tx, identity.Organization.ID, message.ID, mentions); err != nil {
			return err
		}
		if err := a.scheduleGroupAgents(ctx, tx, identity.Organization.ID, normalized.ConversationID, message.ID, sendContext.SubjectID, reply, mentions); err != nil {
			return err
		}
		if err := conversationaction.AdvanceConversationUserReadState(ctx, tx, &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: normalized.ConversationID,
			UserID: identity.User.ID, LastReadMessageID: &message.ID,
		}, message); err != nil {
			return err
		}
		result = conversationaction.MemberConversationMessage(message, sendContext.SubjectID, identity.OrganizationIdentity)
		result.ReplyTo = reply
		result.Mentions = mentions
		result.MentionAll = normalized.MentionAll
		return nil
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, fmt.Errorf("send group message: %w", err)
	}
	return result, nil
}

// normalizeGroupTextMessageInput 规范化群聊文本、引用和提醒参数。
func normalizeGroupTextMessageInput(input GroupTextMessageInput) (GroupTextMessageInput, map[string]conversationaction.ValidationCode) {
	normalized, fields := conversationaction.NormalizeInternalTextMessageInput(conversationaction.InternalTextMessageFields{
		ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
	})
	input.ConversationID, input.ClientMessageID, input.Body, input.ReplyToMessageID = normalized.ConversationID, normalized.ClientMessageID, normalized.Body, normalized.ReplyToMessageID
	if len(input.MentionSubjectIDs) > maxGroupParticipantCount-1 {
		fields["mentionSubjectIds"] = ValidationMentionSubjectIDsInvalid
	}
	seen := make(map[string]struct{}, len(input.MentionSubjectIDs))
	mentionSubjectIDs := make([]string, 0, len(input.MentionSubjectIDs))
	for _, subjectID := range input.MentionSubjectIDs {
		normalized, valid := common.NormalizeUUID(subjectID)
		if !valid {
			fields["mentionSubjectIds"] = ValidationMentionSubjectIDsInvalid
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			fields["mentionSubjectIds"] = ValidationMentionSubjectIDsInvalid
			continue
		}
		seen[normalized] = struct{}{}
		mentionSubjectIDs = append(mentionSubjectIDs, normalized)
	}
	// 提醒顺序决定被点名 AI 员工的发言先后，按发送时的顺序保留。
	input.MentionSubjectIDs = mentionSubjectIDs
	return input, fields
}

// normalizeGroupConversationInput 规范化并校验群聊资料和初始成员。
func normalizeGroupConversationInput(currentIdentityID string, input GroupConversationInput) (GroupConversationInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.ImageFileID = strings.TrimSpace(input.ImageFileID)
	if utf8.RuneCountInString(input.Title) > maxGroupTitleLength {
		fields["title"] = ValidationGroupTitleTooLong
	}
	if utf8.RuneCountInString(input.Description) > maxGroupDescriptionLength {
		fields["description"] = ValidationGroupDescriptionTooLong
	}
	if input.ImageFileID != "" {
		var valid bool
		input.ImageFileID, valid = common.NormalizeUUID(input.ImageFileID)
		if !valid {
			fields["imageFileId"] = ValidationGroupImageFileIDInvalid
		}
	}
	if len(input.MemberIdentityIDs) == 0 {
		fields["memberIdentityIds"] = ValidationGroupMembersRequired
	} else if len(input.MemberIdentityIDs)+1 > maxGroupParticipantCount {
		fields["memberIdentityIds"] = ValidationGroupMembersTooMany
	}
	seen := make(map[string]struct{}, len(input.MemberIdentityIDs))
	for index, identityID := range input.MemberIdentityIDs {
		normalized, valid := common.NormalizeUUID(identityID)
		if !valid || normalized == currentIdentityID {
			fields["memberIdentityIds"] = ValidationGroupMemberIDsInvalid
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			fields["memberIdentityIds"] = ValidationGroupMemberIDsInvalid
			continue
		}
		seen[normalized] = struct{}{}
		input.MemberIdentityIDs[index] = normalized
	}
	return input, fields
}

// loadActiveGroupMembers 读取同企业可加入群聊的有效真人、AI 员工与当前成员本人名下的助理；调用方须在锁定群聊前调用，先对其中 AI 的记录取共享锁，与停用 AI 及其主人的锁序一致。
func loadActiveGroupMembers(ctx context.Context, db bun.IDB, identity *servermodels.Identity, identityIDs []string) ([]groupMemberRow, error) {
	var lockedAgentIDs []string
	if err := db.NewSelect().Model((*servermodels.Agent)(nil)).Column("a.id").
		Where("a.organization_id = ? AND a.identity_id IN (?)", identity.Organization.ID, bun.In(identityIDs)).
		OrderExpr("a.id ASC").For("SHARE").
		Scan(ctx, &lockedAgentIDs); err != nil {
		return nil, fmt.Errorf("lock group agent members: %w", err)
	}
	rows := make([]groupMemberRow, 0, len(identityIDs))
	if err := db.NewSelect().
		TableExpr("organization_identities AS oi").
		ColumnExpr("oi.id AS identity_id").
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		ColumnExpr("? AS assistant_owner_name", conversationaction.AssistantOwnerName("oi")).
		Join("LEFT JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
		Join("LEFT JOIN agents AS a ON a.organization_id = oi.organization_id AND a.identity_id = oi.id").
		Where("oi.organization_id = ?", identity.Organization.ID).
		Where("(oi.type = ? AND u.status = ?) OR (oi.type = ? AND a.status = ?) OR (oi.type = ? AND a.status = ? AND a.owner_user_id = ?)",
			domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive, domain.OrganizationIdentityTypeAgent, domain.IdentityStatusActive,
			domain.OrganizationIdentityTypeAssistant, domain.IdentityStatusActive, identity.User.ID).
		Where("oi.id IN (?)", bun.In(identityIDs)).
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load active group members: %w", err)
	}
	if len(rows) != len(identityIDs) {
		return nil, conversationaction.ErrGroupMemberNotFound
	}
	return rows, nil
}

// scheduleGroupAgents 按引用目标优先、提醒顺序在后的次序为群内 AI 员工与助理追加输入。
func (a *SendGroupTextMessageAction) scheduleGroupAgents(ctx context.Context, db bun.IDB, organizationID, conversationID, messageID, senderSubjectID string, reply *conversationaction.ConversationMessageReference, mentions []conversationaction.ConversationMessageMention) error {
	agentIdentityIDs := make([]string, 0, len(mentions)+1)
	seen := make(map[string]struct{}, len(mentions)+1)
	// 回复 AI 员工或助理的文本消息与显式点名等价，作为首个执行目标。
	if reply != nil && reply.Sender != nil && reply.Sender.IdentityType != nil &&
		domain.OrganizationIdentityTypeIsAI(*reply.Sender.IdentityType) {
		agentIdentityIDs = append(agentIdentityIDs, reply.Sender.SourceID)
		seen[reply.Sender.SourceID] = struct{}{}
	}
	for _, mention := range mentions {
		if !domain.OrganizationIdentityTypeIsAI(mention.IdentityType) {
			continue
		}
		if _, duplicate := seen[mention.SourceID]; duplicate {
			continue
		}
		seen[mention.SourceID] = struct{}{}
		agentIdentityIDs = append(agentIdentityIDs, mention.SourceID)
	}
	if len(agentIdentityIDs) == 0 {
		return nil
	}
	if err := ensureAssistantsReachable(ctx, db, organizationID, agentIdentityIDs); err != nil {
		return err
	}
	if err := a.agentScheduler.ScheduleGroupMentions(ctx, db, organizationID, conversationID, messageID, senderSubjectID, agentIdentityIDs); err != nil {
		return fmt.Errorf("schedule group agent mentions: %w", err)
	}
	return nil
}
