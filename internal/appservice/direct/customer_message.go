//go:build server

// 客户消息发送与客服周期操作。
package direct

import (
	"context"
	"errors"

	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// SendServiceTextMessage 发送成员服务会话文本消息。
func (o *directOperations) SendServiceTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceTextMessageInput) (appservice.ConversationMessage, error) {
	// 翻译发送先按发送编号沿用已发出的译文，重试直接返回已保存的结果；未发出时，预览过的译文须仍是当前回复语言，未预览则把回复译为客户语言，客户语言与客服语言相同时按原文发送。
	var translation *servicesessionaction.OutgoingTranslation
	if input.Translation != nil || input.Translate {
		saved, err := o.sendServiceTextMessage.SavedTranslation(ctx, identity, input.ClientMessageID)
		if err != nil {
			return appservice.ConversationMessage{}, serviceTextMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
		}
		translation = saved
	}
	if translation == nil && input.Translation != nil {
		if err := o.translator.ValidateReplyLanguage(ctx, identity, conversationID, input.Translation.Language); err != nil {
			return appservice.ConversationMessage{}, translationError(ctx, meta, err, i18n.ErrorTranslationFailed, identity.Organization.ID, conversationID)
		}
		translation = &servicesessionaction.OutgoingTranslation{
			Language: input.Translation.Language, SourceLanguage: translationaction.ViewerLanguage(identity), Body: input.Translation.Body,
		}
	} else if translation == nil && input.Translate {
		translated, err := o.translator.TranslateReply(ctx, identity, conversationID, input.Body)
		if err != nil {
			return appservice.ConversationMessage{}, translationError(ctx, meta, err, i18n.ErrorTranslationFailed, identity.Organization.ID, conversationID)
		}
		if translated != nil {
			translation = &servicesessionaction.OutgoingTranslation{Language: translated.Language, SourceLanguage: translated.SourceLanguage, Body: translated.Body}
		}
	}
	message, err := o.sendServiceTextMessage.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
		Visibility: domain.MessageVisibility(input.Visibility), MentionIdentityIDs: input.MentionIdentityIDs, Translation: translation,
	})
	if err != nil {
		return appservice.ConversationMessage{}, serviceTextMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	slog.Info("成员客户文本消息已保存",
		"organization_id", identity.Organization.ID,
		"conversation_id", conversationID,
		"message_id", message.ID,
		"visibility", message.Visibility,
		"sender_identity_id", identity.OrganizationIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// SendServiceAttachmentMessage 发送服务会话附件消息。
func (o *directOperations) SendServiceAttachmentMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceAttachmentMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendServiceAttachmentMessage.Execute(ctx, identity, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, FileID: input.FileID, Body: input.Body,
		ReplyToMessageID: input.ReplyToMessageID, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight,
	})
	if err != nil {
		return appservice.ConversationMessage{}, serviceTextMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	slog.Info("成员客户附件消息已保存",
		"organization_id", identity.Organization.ID,
		"conversation_id", conversationID,
		"message_id", message.ID,
		"file_id", input.FileID,
		"sender_identity_id", identity.OrganizationIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// ClaimServiceSession 领取或接管服务会话最新处理周期。
func (o *directOperations) ClaimServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.claimServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return customerServiceSessionFromAction(result), nil
}

// TransferServiceSession 把当前负责的处理周期转给成员、团队队列或公共队列。
func (o *directOperations) TransferServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.TransferServiceSessionInput) (appservice.ServiceSession, error) {
	result, err := o.transferServiceSession.Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetKind(input.Kind),
		TeamID: input.TeamID, IdentityID: input.IdentityID,
	})
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return customerServiceSessionFromAction(result), nil
}

// CloseServiceSession 关闭服务会话最新处理周期。
func (o *directOperations) CloseServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.closeServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return customerServiceSessionFromAction(result), nil
}

// ReopenServiceSession 重新打开服务会话最新处理周期并分配给当前身份。
func (o *directOperations) ReopenServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.reopenServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return customerServiceSessionFromAction(result), nil
}

// customerServiceSessionFromAction 转换服务周期命令结果。
func customerServiceSessionFromAction(result servicesessionaction.ServiceSessionResult) appservice.ServiceSession {
	var assignee *appservice.InboxAssignee
	if result.Assignee != nil {
		assignee = &appservice.InboxAssignee{IdentityID: result.Assignee.IdentityID, Type: appservice.OrganizationIdentityType(result.Assignee.Type), DisplayName: result.Assignee.DisplayName}
	}
	return appservice.ServiceSession{ID: result.ID, Status: appservice.ServiceSessionStatus(result.Status), Assignee: assignee, ClosedAt: result.ClosedAt}
}

// serviceSessionMutationError 转换服务周期命令错误。
func serviceSessionMutationError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		messageKey := i18n.ErrorServiceSessionNotReplyable
		switch conflictError.Reason {
		case servicesessionaction.ConflictReasonServiceHandlingRequired:
			messageKey = i18n.ErrorServiceHandlingRequired
		case conversationaction.ConflictReasonServiceSessionOwned:
			messageKey = i18n.ErrorServiceSessionOwned
		case servicesessionaction.ConflictReasonServiceSessionOwnRequest:
			messageKey = i18n.ErrorServiceSessionOwnRequest
		case servicesessionaction.ConflictReasonServiceSessionAlreadyOpen:
			messageKey = i18n.ErrorServiceSessionAlreadyOpen
		case servicesessionaction.ConflictReasonTransferTeamUnavailable:
			messageKey = i18n.ErrorTransferTeamUnavailable
		}
		return appservice.ConflictError(meta, messageKey, conflictError.Reason)
	}
	slog.Warn("客服处理周期操作失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorServiceSessionUpdateFailed)
}

// serviceTextMessageError 转换成员客户消息发送错误。
func serviceTextMessageError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if errors.Is(err, fileaction.ErrFileNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		return appservice.ConflictError(meta, customerReplyConflictMessageKey(conflictError.Reason), conflictError.Reason)
	}
	slog.Warn("发送成员客户消息失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorMessageSendFailed)
}

// customerReplyConflictMessageKey 返回对客回复资格冲突的本地化文案键。
func customerReplyConflictMessageKey(reason string) i18n.Key {
	switch reason {
	case servicesessionaction.ConflictReasonServiceHandlingRequired:
		return i18n.ErrorServiceHandlingRequired
	case conversationaction.ConflictReasonServiceSessionOwned:
		return i18n.ErrorServiceSessionOwned
	case servicesessionaction.ConflictReasonServiceSessionOwnRequest:
		return i18n.ErrorServiceSessionOwnRequest
	case conversationaction.ConflictReasonServiceSessionNotReplyable:
		return i18n.ErrorServiceSessionNotReplyable
	case conversationaction.ConflictReasonChannelOutboundUnavailable:
		return i18n.ErrorChannelOutboundUnavailable
	case conversationaction.ConflictReasonChannelOutboundUnsupported:
		return i18n.ErrorChannelOutboundUnsupported
	case conversationaction.ConflictReasonReplyTargetInvalid:
		return i18n.ErrorReplyTargetInvalid
	case servicesessionaction.ConflictReasonChannelAttachmentUnsupported:
		return i18n.ErrorChannelAttachmentUnsupported
	case conversationaction.ConflictReasonAttachmentTooLarge:
		return i18n.ErrorAttachmentTooLarge
	case servicesessionaction.ConflictReasonCaptionTooLong:
		return i18n.ErrorAttachmentCaptionTooLong
	case servicesessionaction.ConflictReasonTranslationTooLong:
		return i18n.ErrorTranslationTooLong
	case servicesessionaction.ConflictReasonNoteMentionTargetInvalid:
		return i18n.ErrorNoteMentionTargetInvalid
	}
	return i18n.ErrorMessageConflict
}
