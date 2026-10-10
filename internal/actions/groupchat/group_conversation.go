//go:build server

package groupchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/membersend"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// maxGroupParticipantCount 是群聊成员数上限，含群主。
const maxGroupParticipantCount = 100

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
	enqueuer       servertask.TxEnqueuer
	agentScheduler conversationaction.GroupAgentMessageScheduler
}

// groupMemberRow 是可加入群聊的成员身份。
type groupMemberRow struct {
	IdentityID              string                       `bun:"identity_id"`
	IdentityType            domain.WorkspaceIdentityType `bun:"identity_type"`
	Personal                bool                         `bun:"personal"`
	DisplayName             string                       `bun:"display_name"`
	AvatarFileID            *string                      `bun:"avatar_file_id"`
	PersonalResponsibleName *string                      `bun:"personal_responsible_name"`
}

// groupParticipantRow 是群聊当前参与者及其身份资料。
type groupParticipantRow struct {
	IdentityType                  domain.WorkspaceIdentityType `bun:"identity_type"`
	ChatSubjectID                 string                       `bun:"chat_subject_id"`
	IdentityID                    string                       `bun:"identity_id"`
	DisplayName                   string                       `bun:"display_name"`
	AvatarFileID                  *string                      `bun:"avatar_file_id"`
	Role                          string                       `bun:"role"`
	PersonalResponsibleName       *string                      `bun:"personal_responsible_name"`
	PersonalResponsibleIdentityID *string                      `bun:"personal_responsible_identity_id"`
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
func NewSendGroupTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, agentScheduler conversationaction.GroupAgentMessageScheduler) *SendGroupTextMessageAction {
	return &SendGroupTextMessageAction{db: db, enqueuer: enqueuer, agentScheduler: agentScheduler}
}

// Execute 创建包含有效企业成员的企业内部群聊。
func (a *CreateGroupConversationAction) Execute(ctx context.Context, identity *servermodels.Identity, input GroupConversationInput) (GroupConversationSummary, error) {
	normalized, fields := normalizeGroupConversationInput(identity.WorkspaceIdentity.ID, input)
	if len(fields) > 0 {
		return GroupConversationSummary{}, &conversationaction.ValidationError{Fields: fields}
	}

	conversationID := uuid.NewV7().String()
	participantIDs := make(map[string]string, len(normalized.MemberIdentityIDs)+1)
	for _, identityID := range append([]string{identity.WorkspaceIdentity.ID}, normalized.MemberIdentityIDs...) {
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
		previewNames = arr.OrEmpty(arr.Map(members[:min(len(members), messagequery.GroupMemberPreviewNamesLimit)], func(member groupMemberRow) string {
			return member.DisplayName
		}))
		var imageFileID *string
		if normalized.ImageFileID != "" {
			imageFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Workspace.ID, domain.FilePurposeGroupImage, normalized.ImageFileID, nil)
			if err != nil {
				return err
			}
		}
		subjects, err := chatstate.EnsureSubjects(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, append([]string{identity.WorkspaceIdentity.ID}, normalized.MemberIdentityIDs...))
		if err != nil {
			return err
		}
		creatorSubject := subjects[identity.WorkspaceIdentity.ID]
		createdBySubjectID := creatorSubject.ID
		conversation := &servermodels.Conversation{
			ID: conversationID, WorkspaceID: identity.Workspace.ID,
			Type: string(domain.ConversationTypeGroup), Status: string(domain.ConversationStatusActive),
			Title: support.NilIfZero(normalized.Title), Description: support.NilIfZero(normalized.Description),
			ImageFileID: imageFileID, CreatedBySubjectID: &createdBySubjectID, Version: 1,
		}
		if _, err := tx.NewInsert().Model(conversation).
			Column("id", "workspace_id", "type", "status", "title", "description", "image_file_id", "created_by_subject_id", "version").
			Exec(ctx); err != nil {
			return fmt.Errorf("create group conversation: %w", err)
		}

		participants := make([]*servermodels.ConversationParticipant, 0, len(members)+1)
		participants = append(participants, &servermodels.ConversationParticipant{
			ID: participantIDs[identity.WorkspaceIdentity.ID], WorkspaceID: identity.Workspace.ID,
			ConversationID: conversation.ID, SubjectID: creatorSubject.ID,
			Role: string(domain.ConversationParticipantRoleOwner),
		})
		for _, member := range members {
			subject := subjects[member.IdentityID]
			participants = append(participants, &servermodels.ConversationParticipant{
				ID: participantIDs[member.IdentityID], WorkspaceID: identity.Workspace.ID,
				ConversationID: conversation.ID, SubjectID: subject.ID,
				Role: string(domain.ConversationParticipantRoleMember),
			})
		}
		if _, err := tx.NewInsert().Model(&participants).
			Column("id", "workspace_id", "conversation_id", "subject_id", "role").
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
		ImageFileID: support.NilIfZero(normalized.ImageFileID), MemberPreviewNames: previewNames,
	}, nil
}

// Execute 委托共用查询返回当前成员可见的群聊。
func (q *GetGroupConversationQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (GroupConversation, error) {
	return loadGroupConversation(ctx, q.db, identity, conversationID)
}

// loadGroupConversation 返回当前成员可见的群聊资料和有效参与者。
func loadGroupConversation(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) (GroupConversation, error) {
	conversationID, _ = str.NormalizeUUID(conversationID)
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
		ColumnExpr(messagequery.GroupMemberPreviewNamesExpr+" AS member_preview_names", identity.WorkspaceIdentity.ID, messagequery.GroupMemberPreviewNamesLimit).
		ColumnExpr("COALESCE(cv.description, '') AS description").
		ColumnExpr("cv.image_file_id::text AS image_file_id").
		ColumnExpr("cv.status AS status").
		ColumnExpr("cv.created_at AS created_at").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("state.archived_at").
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
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
		ColumnExpr("? AS personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		ColumnExpr("? AS personal_responsible_identity_id", chatstate.PersonalResponsibleColumn("oi", "id")).
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.workspace_id = cs.workspace_id AND oi.id = cs.source_id").
		Where("cp.workspace_id = ?", identity.Workspace.ID).
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
			PersonalResponsibleName: row.PersonalResponsibleName, PersonalResponsibleIdentityID: row.PersonalResponsibleIdentityID,
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
	normalized := normalizeGroupTextMessageInput(input)
	intent := membersend.InternalText(normalized.ConversationID, normalized.Body, normalized.ReplyToMessageID)
	intent.MentionAll = normalized.MentionAll
	intent.MentionSubjectIDs = append([]string{}, normalized.MentionSubjectIDs...)
	key := membersend.Key(identity, normalized.ClientMessageID)
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 发送资格校验前先按发送编号读取本人保存的消息。
		saved, found, err := membersend.Replay(ctx, tx, identity, intent, key)
		if err != nil || found {
			result = saved
			return err
		}
		sendContext, err := chatstate.LockGroup(ctx, tx, identity, normalized.ConversationID, chatstate.GroupSendable)
		if err != nil {
			return err
		}
		reply, err := conversationaction.LoadConversationReplyTarget(ctx, tx, identity.Workspace.ID, normalized.ConversationID, normalized.ReplyToMessageID)
		if err != nil {
			return err
		}
		mentions, err := loadGroupMentionTargets(ctx, tx, identity.Workspace.ID, normalized.ConversationID, sendContext.SubjectID, normalized.MentionSubjectIDs)
		if err != nil {
			return err
		}
		message := membersend.NewMessage(identity, membersend.Draft{
			ConversationID: normalized.ConversationID, ParticipantID: sendContext.ParticipantID, ClientMessageID: normalized.ClientMessageID,
			Type: domain.MessageTypeText, Body: normalized.Body,
		})
		message.MentionAll = normalized.MentionAll
		if reply != nil {
			message.ReplyToMessageID = &reply.ID
		}
		message, inserted, err := chatstate.AppendMessage(ctx, tx, a.enqueuer, sendContext.Conversation, message)
		if err != nil {
			return err
		}
		if !inserted {
			result, _, err = membersend.Replay(ctx, tx, identity, intent, key)
			return err
		}
		if err := conversationaction.CreateMessageMentions(ctx, tx, identity.Workspace.ID, message.ID, mentions); err != nil {
			return err
		}
		if err := a.scheduleGroupAgents(ctx, tx, identity.Workspace.ID, normalized.ConversationID, message.ID, sendContext.SubjectID, reply, mentions); err != nil {
			return err
		}
		if err := membersend.MarkSenderRead(ctx, tx, identity, message); err != nil {
			return err
		}
		result = conversationaction.MemberConversationMessage(message, sendContext.SubjectID, identity.WorkspaceIdentity)
		result.ReplyTo = reply
		result.Mentions = mentions
		return nil
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, fmt.Errorf("send group message: %w", err)
	}
	return result, nil
}

// normalizeGroupTextMessageInput 规范化群聊文本、引用和提醒参数，提醒对象按发送顺序去重。
func normalizeGroupTextMessageInput(input GroupTextMessageInput) GroupTextMessageInput {
	normalized := conversationaction.NormalizeInternalTextMessageInput(conversationaction.InternalTextMessageFields{
		ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
	})
	input.ConversationID, input.ClientMessageID, input.Body, input.ReplyToMessageID = normalized.ConversationID, normalized.ClientMessageID, normalized.Body, normalized.ReplyToMessageID
	var seen set.Set[string]
	mentionSubjectIDs := make([]string, 0, len(input.MentionSubjectIDs))
	for _, subjectID := range input.MentionSubjectIDs {
		// 提醒顺序决定被点名 AI 员工的发言先后，按发送时的顺序保留。
		if normalized, _ := str.NormalizeUUID(subjectID); seen.Add(normalized) {
			mentionSubjectIDs = append(mentionSubjectIDs, normalized)
		}
	}
	input.MentionSubjectIDs = mentionSubjectIDs
	return input
}

// normalizeGroupConversationInput 规范化群聊资料和初始成员并按顺序去重，初始成员不得包含当前成员。
func normalizeGroupConversationInput(currentIdentityID string, input GroupConversationInput) (GroupConversationInput, map[string]conversationaction.ValidationCode) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.ImageFileID != "" {
		input.ImageFileID, _ = str.NormalizeUUID(input.ImageFileID)
	}
	_, memberIDs, fields := normalizeGroupMembersInput(currentIdentityID, "", input.MemberIdentityIDs)
	input.MemberIdentityIDs = memberIDs
	return input, fields
}

// loadActiveGroupMembers 读取同企业可加入群聊的有效真人、服务型 AI 员工与当前成员负责的个人 AI 员工；调用方须在锁定群聊前调用，先对其中 AI 员工的记录取共享锁，与停用 AI 员工及其负责人的锁序一致。
func loadActiveGroupMembers(ctx context.Context, db bun.IDB, identity *servermodels.Identity, identityIDs []string) ([]groupMemberRow, error) {
	var lockedAgentIDs []string
	if err := db.NewSelect().Model((*servermodels.Agent)(nil)).Column("a.id").
		Where("a.workspace_id = ? AND a.identity_id IN (?)", identity.Workspace.ID, bun.List(identityIDs)).
		OrderExpr("a.id ASC").For("SHARE").
		Scan(ctx, &lockedAgentIDs); err != nil {
		return nil, fmt.Errorf("lock group agent members: %w", err)
	}
	rows := make([]groupMemberRow, 0, len(identityIDs))
	if err := db.NewSelect().
		TableExpr("workspace_identities AS oi").
		ColumnExpr("oi.id AS identity_id").
		ColumnExpr("oi.type AS identity_type").
		ColumnExpr("COALESCE(? = ANY(a.service_audiences), FALSE) AS personal", domain.ServiceAudiencePersonal).
		ColumnExpr("oi.display_name AS display_name").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		ColumnExpr("? AS personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
		Join("LEFT JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
		Join("LEFT JOIN agents AS a ON a.workspace_id = oi.workspace_id AND a.identity_id = oi.id").
		Where("oi.workspace_id = ?", identity.Workspace.ID).
		Where("(oi.type = ? AND u.status = ?) OR (oi.type = ? AND a.status = ? AND (NOT ? = ANY(a.service_audiences) OR a.responsible_user_id = ?))",
			domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive, domain.WorkspaceIdentityTypeAgent, domain.IdentityStatusActive,
			domain.ServiceAudiencePersonal, identity.User.ID).
		Where("oi.id IN (?)", bun.List(identityIDs)).
		OrderExpr("lower(oi.display_name) ASC, oi.id ASC").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load active group members: %w", err)
	}
	if len(rows) != len(identityIDs) {
		return nil, conversationaction.ErrGroupMemberNotFound
	}
	return rows, nil
}

// scheduleGroupAgents 按引用目标优先、提醒顺序在后的次序为群内 AI 员工追加输入。
func (a *SendGroupTextMessageAction) scheduleGroupAgents(ctx context.Context, db bun.IDB, workspaceID, conversationID, messageID, senderSubjectID string, reply *conversationaction.ConversationMessageReference, mentions []conversationaction.ConversationMessageMention) error {
	var agentIdentityIDs []string
	// 回复 AI 员工的文本消息与显式点名等价，作为首个执行目标。
	if reply != nil && reply.Sender != nil && reply.Sender.IdentityType != nil &&
		*reply.Sender.IdentityType == domain.WorkspaceIdentityTypeAgent {
		agentIdentityIDs = append(agentIdentityIDs, reply.Sender.SourceID)
	}
	agentIdentityIDs = arr.Unique(append(agentIdentityIDs, arr.FilterMap(mentions, func(mention conversationaction.ConversationMessageMention) (string, bool) {
		return mention.SourceID, mention.IdentityType == domain.WorkspaceIdentityTypeAgent
	})...))
	if len(agentIdentityIDs) == 0 {
		return nil
	}
	if err := ensurePersonalAgentsReachable(ctx, db, workspaceID, agentIdentityIDs); err != nil {
		return err
	}
	if err := a.agentScheduler.ScheduleGroupMentions(ctx, db, workspaceID, conversationID, messageID, senderSubjectID, agentIdentityIDs); err != nil {
		return fmt.Errorf("schedule group agent mentions: %w", err)
	}
	return nil
}
