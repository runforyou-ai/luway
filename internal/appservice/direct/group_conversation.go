//go:build server

// 群聊资料、成员管理与群消息发送。
package direct

import (
	"context"
	"errors"

	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// CreateGroupConversation 创建包含有效企业成员的企业内部群聊。
func (o *directOperations) CreateGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.GroupConversationInput) (appservice.InboxConversation, error) {
	summary, err := o.createGroupConversation.Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: input.Title, Description: input.Description, ImageFileID: input.ImageFileID,
		MemberIdentityIDs: input.MemberIdentityIDs,
	})
	if err != nil {
		return appservice.InboxConversation{}, groupConversationError(meta, err, "create")
	}
	slog.Info("企业内部群聊已创建",
		"organization_id", identity.Organization.ID,
		"conversation_id", summary.ID,
		"member_count", summary.MemberCount,
	)
	imageFileIDs := make([]string, 0, 1)
	if summary.ImageFileID != nil {
		imageFileIDs = append(imageFileIDs, *summary.ImageFileID)
	}
	imageURLs, imageErr := o.activeFileURLs(ctx, identity, imageFileIDs)
	if imageErr != nil {
		slog.Warn("读取新建群聊图片失败", "organization_id", identity.Organization.ID, "conversation_id", summary.ID, "error", imageErr)
	}
	return appservice.InboxConversation{
		ID: summary.ID, Type: appservice.ConversationTypeGroup,
		Group: &appservice.GroupInboxConversation{
			Title: summary.Title, ImageURL: optionalFileURL(imageURLs, summary.ImageFileID),
			Status: appservice.ConversationStatus(summary.Status), MemberCount: summary.MemberCount,
			MemberPreviewNames: summary.MemberPreviewNames,
		},
	}, nil
}

// GetGroupConversation 返回当前成员可见的群聊资料和有效成员。
func (o *directOperations) GetGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.GroupConversation, error) {
	record, err := o.getGroupConversation.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "get")
	}
	result, err := o.groupConversationFromAction(ctx, identity, record)
	if err != nil {
		return appservice.GroupConversation{}, appservice.FailedError(meta, i18n.ErrorGroupConversationReadFailed, err)
	}
	return result, nil
}

// UpdateGroupConversation 修改群聊资料。
func (o *directOperations) UpdateGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationProfileInput) (appservice.GroupConversation, error) {
	record, err := o.updateGroupConversation.Execute(ctx, identity, groupchataction.GroupConversationProfileInput{
		ConversationID: conversationID, Title: input.Title, Description: input.Description, ImageFileID: input.ImageFileID,
	})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "update")
	}
	slog.Info("企业群聊资料已修改", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// AddGroupConversationMembers 批量增加群聊成员。
func (o *directOperations) AddGroupConversationMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationMembersInput) (appservice.GroupConversation, error) {
	record, err := o.addGroupConversationMembers.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: conversationID, MemberIdentityIDs: input.MemberIdentityIDs})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "add_members")
	}
	slog.Info("企业群聊成员已增加", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID, "added_count", len(input.MemberIdentityIDs))
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// RemoveGroupConversationMember 移除单个群聊成员。
func (o *directOperations) RemoveGroupConversationMember(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationMemberInput) (appservice.GroupConversation, error) {
	record, err := o.removeGroupConversationMember.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: conversationID, MemberIdentityID: input.MemberIdentityID})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "remove_member")
	}
	slog.Info("企业群聊成员已移除", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID, "member_identity_id", input.MemberIdentityID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// TransferGroupConversationOwner 转让群主。
func (o *directOperations) TransferGroupConversationOwner(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationOwnerInput) (appservice.GroupConversation, error) {
	record, err := o.transferGroupConversationOwner.Execute(ctx, identity, groupchataction.GroupConversationOwnerInput{ConversationID: conversationID, OwnerIdentityID: input.OwnerIdentityID})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "transfer_owner")
	}
	slog.Info("企业群聊群主已转让", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID, "owner_identity_id", input.OwnerIdentityID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// LeaveGroupConversation 退出普通成员参与的群聊。
func (o *directOperations) LeaveGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) error {
	err := o.leaveGroupConversation.Execute(ctx, identity, conversationID)
	if err != nil {
		return groupConversationError(meta, err, "leave")
	}
	slog.Info("企业群聊退出操作已完成", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID)
	return nil
}

// DissolveGroupConversation 解散群聊并返回只读资料。
func (o *directOperations) DissolveGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.GroupConversation, error) {
	record, err := o.dissolveGroupConversation.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "dissolve")
	}
	slog.Info("企业群聊解散操作已完成", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "operator_identity_id", identity.OrganizationIdentity.ID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// groupConversationMutationResult 转换群聊管理命令结果。
func (o *directOperations) groupConversationMutationResult(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, record groupchataction.GroupConversation) (appservice.GroupConversation, error) {
	result, err := o.groupConversationFromAction(ctx, identity, record)
	if err != nil {
		return appservice.GroupConversation{}, appservice.FailedError(meta, i18n.ErrorGroupConversationReadFailed, err)
	}
	return result, nil
}

// groupConversationFromAction 转换群聊资料并生成群图片和成员头像地址。
func (o *directOperations) groupConversationFromAction(ctx context.Context, identity *servermodels.Identity, record groupchataction.GroupConversation) (appservice.GroupConversation, error) {
	avatarFileIDs := make([]string, 0, len(record.Participants)+1)
	if record.ImageFileID != nil {
		avatarFileIDs = append(avatarFileIDs, *record.ImageFileID)
	}
	for _, participant := range record.Participants {
		if participant.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *participant.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.GroupConversation{}, err
	}
	participants := make([]appservice.GroupParticipant, 0, len(record.Participants))
	for _, participant := range record.Participants {
		participants = append(participants, appservice.GroupParticipant{
			ChatSubjectID: participant.ChatSubjectID, IdentityType: appservice.OrganizationIdentityType(participant.IdentityType), IdentityID: participant.IdentityID, DisplayName: participant.DisplayName,
			AvatarURL: optionalFileURL(avatarURLs, participant.AvatarFileID), Role: appservice.GroupParticipantRole(participant.Role),
			PersonalResponsibleName: participant.PersonalResponsibleName, PersonalResponsibleIdentityID: participant.PersonalResponsibleIdentityID,
		})
	}
	return appservice.GroupConversation{
		ID: record.ID, Title: record.Title, Description: record.Description,
		ImageURL: optionalFileURL(avatarURLs, record.ImageFileID), Status: appservice.ConversationStatus(record.Status),
		CreatedAt: record.CreatedAt, Participants: participants,
		Muted: record.Muted, ArchivedAt: record.ArchivedAt, MemberPreviewNames: record.MemberPreviewNames,
	}, nil
}

// SendGroupTextMessage 发送企业内部群聊文本消息。
func (o *directOperations) SendGroupTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupTextMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendGroupTextMessage.Execute(ctx, identity, groupchataction.GroupTextMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body,
		ReplyToMessageID: input.ReplyToMessageID, MentionSubjectIDs: input.MentionSubjectIDs, MentionAll: input.MentionAll,
	})
	if err != nil {
		return appservice.ConversationMessage{}, groupConversationError(meta, err, "send")
	}
	slog.Info("企业内部群聊文本消息已保存",
		"organization_id", identity.Organization.ID,
		"conversation_id", conversationID,
		"message_id", message.ID,
		"sender_identity_id", identity.OrganizationIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// groupConversationError 转换企业群聊命令错误。
func groupConversationError(meta appservice.RequestMeta, err error, operation string) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, conversationaction.ErrGroupMemberNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorGroupMemberNotFound)
	}
	if errors.Is(err, fileaction.ErrLinkedImageNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	if errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
		return appservice.ForbiddenError(meta, i18n.ErrorGroupOwnerRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		messageKey := i18n.ErrorMessageConflict
		switch conflictError.Reason {
		case groupchataction.ConflictReasonGroupMemberAlreadyActive:
			messageKey = i18n.ErrorGroupMemberAlreadyActive
		case groupchataction.ConflictReasonGroupMemberNotActive:
			messageKey = i18n.ErrorGroupMemberNotActive
		case groupchataction.ConflictReasonGroupOwnerCannotBeRemoved:
			messageKey = i18n.ErrorGroupOwnerCannotBeRemoved
		case groupchataction.ConflictReasonGroupOwnerCannotLeave:
			messageKey = i18n.ErrorGroupOwnerCannotLeave
		case conversationaction.ConflictReasonReplyTargetInvalid:
			messageKey = i18n.ErrorReplyTargetInvalid
		case groupchataction.ConflictReasonGroupMentionTargetInvalid:
			messageKey = i18n.ErrorGroupMentionTargetInvalid
		}
		if key, ok := personalAgentConflictKeys[conflictError.Reason]; ok {
			messageKey = key
		}
		return appservice.ConflictError(meta, messageKey, conflictError.Reason)
	}
	switch operation {
	case "create":
		return appservice.FailedError(meta, i18n.ErrorGroupConversationCreateFailed, err)
	case "get":
		return appservice.FailedError(meta, i18n.ErrorGroupConversationReadFailed, err)
	case "leave":
		return appservice.FailedError(meta, i18n.ErrorGroupConversationLeaveFailed, err)
	case "send":
		return appservice.FailedError(meta, i18n.ErrorGroupMessageSendFailed, err)
	default:
		return appservice.FailedError(meta, i18n.ErrorGroupConversationUpdateFailed, err)
	}
}
