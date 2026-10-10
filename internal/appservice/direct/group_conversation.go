//go:build server

// 群聊资料、成员管理与群消息发送。

package direct

import (
	"context"
	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/mapx"
)

// CreateGroupConversation 创建包含有效企业成员的企业内部群聊。
func (o *conversationOps) CreateGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.GroupConversationInput) (appservice.InboxConversation, error) {
	summary, err := o.createGroupConversation.Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: input.Title, Description: input.Description, ImageFileID: input.ImageFileID,
		MemberIdentityIDs: input.MemberIdentityIDs,
	})
	if err != nil {
		return appservice.InboxConversation{}, groupConversationError(meta, err, "create")
	}
	slog.InfoContext(ctx, "企业内部群聊已创建",
		"conversation_id", summary.ID,
		"member_count", summary.MemberCount,
	)
	imageFileIDs := make([]string, 0, 1)
	if summary.ImageFileID != nil {
		imageFileIDs = append(imageFileIDs, *summary.ImageFileID)
	}
	imageURLs, imageErr := o.files.activeFileURLs(ctx, identity, imageFileIDs)
	if imageErr != nil {
		slog.WarnContext(ctx, "读取新建群聊图片失败", "conversation_id", summary.ID, "error", imageErr)
	}
	return appservice.InboxConversation{
		ID: summary.ID, Type: domain.ConversationTypeGroup,
		Group: &appservice.GroupInboxConversation{
			Title: summary.Title, ImageURL: optionalFileURL(imageURLs, summary.ImageFileID),
			Status: summary.Status, MemberCount: summary.MemberCount,
			MemberPreviewNames: summary.MemberPreviewNames,
		},
	}, nil
}

// GetGroupConversation 返回当前成员可见的群聊资料和有效成员。
func (o *conversationOps) GetGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.GroupConversation, error) {
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
func (o *conversationOps) UpdateGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationProfileInput) (appservice.GroupConversation, error) {
	record, err := o.updateGroupConversation.Execute(ctx, identity, groupchataction.GroupConversationProfileInput{
		ConversationID: conversationID, Title: input.Title, Description: input.Description, ImageFileID: input.ImageFileID,
	})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "update")
	}
	slog.InfoContext(ctx, "企业群聊资料已修改", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// AddGroupConversationMembers 批量增加群聊成员。
func (o *conversationOps) AddGroupConversationMembers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationMembersInput) (appservice.GroupConversation, error) {
	record, err := o.addGroupConversationMembers.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: conversationID, MemberIdentityIDs: input.MemberIdentityIDs})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "add_members")
	}
	slog.InfoContext(ctx, "企业群聊成员已增加", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID, "added_count", len(input.MemberIdentityIDs))
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// RemoveGroupConversationMember 移除单个群聊成员。
func (o *conversationOps) RemoveGroupConversationMember(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationMemberInput) (appservice.GroupConversation, error) {
	record, err := o.removeGroupConversationMember.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: conversationID, MemberIdentityID: input.MemberIdentityID})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "remove_member")
	}
	slog.InfoContext(ctx, "企业群聊成员已移除", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID, "member_identity_id", input.MemberIdentityID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// TransferGroupConversationOwner 转让群主。
func (o *conversationOps) TransferGroupConversationOwner(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupConversationOwnerInput) (appservice.GroupConversation, error) {
	record, err := o.transferGroupConversationOwner.Execute(ctx, identity, groupchataction.GroupConversationOwnerInput{ConversationID: conversationID, OwnerIdentityID: input.OwnerIdentityID})
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "transfer_owner")
	}
	slog.InfoContext(ctx, "企业群聊群主已转让", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID, "owner_identity_id", input.OwnerIdentityID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// LeaveGroupConversation 退出普通成员参与的群聊。
func (o *conversationOps) LeaveGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) error {
	err := o.leaveGroupConversation.Execute(ctx, identity, conversationID)
	if err != nil {
		return groupConversationError(meta, err, "leave")
	}
	slog.InfoContext(ctx, "企业群聊退出操作已完成", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID)
	return nil
}

// DissolveGroupConversation 解散群聊并返回只读资料。
func (o *conversationOps) DissolveGroupConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.GroupConversation, error) {
	record, err := o.dissolveGroupConversation.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.GroupConversation{}, groupConversationError(meta, err, "dissolve")
	}
	slog.InfoContext(ctx, "企业群聊解散操作已完成", "conversation_id", conversationID, "operator_identity_id", identity.WorkspaceIdentity.ID)
	return o.groupConversationMutationResult(ctx, meta, identity, record)
}

// groupConversationMutationResult 转换群聊管理命令结果。
func (o *conversationOps) groupConversationMutationResult(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, record groupchataction.GroupConversation) (appservice.GroupConversation, error) {
	result, err := o.groupConversationFromAction(ctx, identity, record)
	if err != nil {
		return appservice.GroupConversation{}, appservice.FailedError(meta, i18n.ErrorGroupConversationReadFailed, err)
	}
	return result, nil
}

// groupConversationFromAction 转换群聊资料并生成群图片和成员头像地址。
func (o *conversationOps) groupConversationFromAction(ctx context.Context, identity *servermodels.Identity, record groupchataction.GroupConversation) (appservice.GroupConversation, error) {
	avatarFileIDs := make([]string, 0, len(record.Participants)+1)
	if record.ImageFileID != nil {
		avatarFileIDs = append(avatarFileIDs, *record.ImageFileID)
	}
	avatarFileIDs = append(avatarFileIDs, arr.FilterMap(record.Participants, func(participant groupchataction.GroupParticipant) (string, bool) {
		return support.Deref(participant.AvatarFileID), participant.AvatarFileID != nil
	})...)
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.GroupConversation{}, err
	}
	participants := arr.Map(record.Participants, func(participant groupchataction.GroupParticipant) appservice.GroupParticipant {
		return appservice.GroupParticipant{
			ChatSubjectID: participant.ChatSubjectID, IdentityType: participant.IdentityType, IdentityID: participant.IdentityID, DisplayName: participant.DisplayName,
			AvatarURL: optionalFileURL(avatarURLs, participant.AvatarFileID), Role: appservice.GroupParticipantRole(participant.Role),
			PersonalResponsibleName: participant.PersonalResponsibleName, PersonalResponsibleIdentityID: participant.PersonalResponsibleIdentityID,
		}
	})
	return appservice.GroupConversation{
		ID: record.ID, Title: record.Title, Description: record.Description,
		ImageURL: optionalFileURL(avatarURLs, record.ImageFileID), Status: record.Status,
		CreatedAt: record.CreatedAt, Participants: participants,
		Muted: record.Muted, ArchivedAt: record.ArchivedAt, MemberPreviewNames: record.MemberPreviewNames,
	}, nil
}

// SendGroupTextMessage 发送企业内部群聊文本消息。
func (o *conversationOps) SendGroupTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.GroupTextMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendGroupTextMessage.Execute(ctx, identity, groupchataction.GroupTextMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body,
		ReplyToMessageID: input.ReplyToMessageID, MentionSubjectIDs: input.MentionSubjectIDs, MentionAll: input.MentionAll,
	})
	if err != nil {
		return appservice.ConversationMessage{}, groupConversationError(meta, err, "send")
	}
	slog.InfoContext(ctx, "企业内部群聊文本消息已保存",
		"conversation_id", conversationID,
		"message_id", message.ID,
		"sender_identity_id", identity.WorkspaceIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// groupConversationConflictKeys 把企业群聊命令的冲突原因映射为本地化文案键。
var groupConversationConflictKeys = mapx.Merge(
	map[string]i18n.Key{
		groupchataction.ConflictReasonGroupMemberAlreadyActive:  i18n.ErrorGroupMemberAlreadyActive,
		groupchataction.ConflictReasonGroupMemberNotActive:      i18n.ErrorGroupMemberNotActive,
		groupchataction.ConflictReasonGroupOwnerCannotBeRemoved: i18n.ErrorGroupOwnerCannotBeRemoved,
		groupchataction.ConflictReasonGroupOwnerCannotLeave:     i18n.ErrorGroupOwnerCannotLeave,
		conversationaction.ConflictReasonReplyTargetInvalid:     i18n.ErrorReplyTargetInvalid,
		groupchataction.ConflictReasonGroupMentionTargetInvalid: i18n.ErrorGroupMentionTargetInvalid,
	},
	personalAgentConflictKeys,
)

// groupConversationErrors 是企业群聊命令的错误转换规则。
var groupConversationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrGroupMemberNotFound, dispatch.NotFound(i18n.ErrorGroupMemberNotFound)),
	dispatch.Is(fileaction.ErrLinkedImageNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.Is(conversationaction.ErrGroupOwnerRequired, dispatch.Forbidden(i18n.ErrorGroupOwnerRequired)),
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.FieldRule(conversationMessageValidationKeys),
	conversationConflictRule(i18n.ErrorMessageConflict, groupConversationConflictKeys),
})

// groupConversationFailureKeys 把企业群聊命令映射为操作失败文案键，未登记的命令按更新失败提示。
var groupConversationFailureKeys = map[string]i18n.Key{
	"create": i18n.ErrorGroupConversationCreateFailed,
	"get":    i18n.ErrorGroupConversationReadFailed,
	"leave":  i18n.ErrorGroupConversationLeaveFailed,
	"send":   i18n.ErrorGroupMessageSendFailed,
}

// groupConversationError 转换企业群聊命令错误。
func groupConversationError(meta appservice.RequestMeta, err error, operation string) error {
	failureKey, ok := groupConversationFailureKeys[operation]
	if !ok {
		failureKey = i18n.ErrorGroupConversationUpdateFailed
	}
	return groupConversationErrors.Translate(meta, err, failureKey)
}
