//go:build server

// 客户消息发送与客服周期操作。

package direct

import (
	"context"
	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
)

// SendServiceTextMessage 发送成员服务会话文本消息。
func (o *conversationOps) SendServiceTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceTextMessageInput) (appservice.ConversationMessage, error) {
	// 翻译发送先按发送编号沿用已发出的译文，重试直接返回已保存的结果；未发出时，预览过的译文须仍是当前回复语言，未预览则把回复译为客户语言，客户语言与客服语言相同时按原文发送。
	var translation *servicesessionaction.OutgoingTranslation
	if input.Translation != nil || input.Translate {
		saved, err := o.sendServiceTextMessage.SavedTranslation(ctx, identity, input.ClientMessageID)
		if err != nil {
			return appservice.ConversationMessage{}, serviceTextMessageError(meta, err)
		}
		translation = saved
	}
	if translation == nil && input.Translation != nil {
		if err := o.translator.ValidateReplyLanguage(ctx, identity, conversationID, input.Translation.Language); err != nil {
			return appservice.ConversationMessage{}, translationError(meta, err, i18n.ErrorTranslationFailed)
		}
		translation = &servicesessionaction.OutgoingTranslation{
			Language: input.Translation.Language, SourceLanguage: translationaction.ViewerLanguage(identity), Body: input.Translation.Body,
		}
	} else if translation == nil && input.Translate {
		translated, err := o.translator.TranslateReply(ctx, identity, conversationID, input.Body)
		if err != nil {
			return appservice.ConversationMessage{}, translationError(meta, err, i18n.ErrorTranslationFailed)
		}
		if translated != nil {
			translation = &servicesessionaction.OutgoingTranslation{Language: translated.Language, SourceLanguage: translated.SourceLanguage, Body: translated.Body}
		}
	}
	message, err := o.sendServiceTextMessage.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
		Visibility: input.Visibility, MentionIdentityIDs: input.MentionIdentityIDs, Translation: translation,
	})
	if err != nil {
		return appservice.ConversationMessage{}, serviceTextMessageError(meta, err)
	}
	slog.InfoContext(ctx, "成员客户文本消息已保存",
		"conversation_id", conversationID,
		"message_id", message.ID,
		"visibility", message.Visibility,
		"sender_identity_id", identity.WorkspaceIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// SendServiceAttachmentMessage 发送服务会话附件消息。
func (o *conversationOps) SendServiceAttachmentMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceAttachmentMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendServiceAttachmentMessage.Execute(ctx, identity, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, FileID: input.FileID, Body: input.Body,
		ReplyToMessageID: input.ReplyToMessageID, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight,
	})
	if err != nil {
		return appservice.ConversationMessage{}, serviceTextMessageError(meta, err)
	}
	slog.InfoContext(ctx, "成员客户附件消息已保存",
		"conversation_id", conversationID,
		"message_id", message.ID,
		"file_id", input.FileID,
		"sender_identity_id", identity.WorkspaceIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// ClaimServiceSession 领取或接管服务会话最新处理周期。
func (o *conversationOps) ClaimServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.claimServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(meta, err)
	}
	return customerServiceSessionFromAction(result), nil
}

// TransferServiceSession 把当前负责的处理周期转给成员、团队队列或公共队列。
func (o *conversationOps) TransferServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.TransferServiceSessionInput) (appservice.ServiceSession, error) {
	result, err := o.transferServiceSession.Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: input.Kind,
		TeamID: input.TeamID, IdentityID: input.IdentityID,
	})
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(meta, err)
	}
	return customerServiceSessionFromAction(result), nil
}

// CloseServiceSession 关闭服务会话最新处理周期。
func (o *conversationOps) CloseServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.closeServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(meta, err)
	}
	return customerServiceSessionFromAction(result), nil
}

// ReopenServiceSession 重新打开服务会话最新处理周期并分配给当前身份。
func (o *conversationOps) ReopenServiceSession(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSession, error) {
	result, err := o.reopenServiceSession.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ServiceSession{}, serviceSessionMutationError(meta, err)
	}
	return customerServiceSessionFromAction(result), nil
}

// customerServiceSessionFromAction 转换服务周期命令结果。
func customerServiceSessionFromAction(result servicesessionaction.ServiceSessionResult) appservice.ServiceSession {
	assignee := support.MapPtr(result.Assignee, func(assignee servicesessionaction.ServiceSessionAssignee) appservice.InboxAssignee {
		return appservice.InboxAssignee{IdentityID: assignee.IdentityID, Type: assignee.Type, DisplayName: assignee.DisplayName}
	})
	return appservice.ServiceSession{ID: result.ID, Status: result.Status, Assignee: assignee, ClosedAt: result.ClosedAt}
}

// serviceSessionConflictKeys 把服务周期命令冲突原因映射为本地化文案键。
var serviceSessionConflictKeys = map[string]i18n.Key{
	servicesessionaction.ConflictReasonServiceHandlingRequired:   i18n.ErrorServiceHandlingRequired,
	conversationaction.ConflictReasonServiceSessionOwned:         i18n.ErrorServiceSessionOwned,
	servicesessionaction.ConflictReasonServiceSessionOwnRequest:  i18n.ErrorServiceSessionOwnRequest,
	servicesessionaction.ConflictReasonServiceSessionAlreadyOpen: i18n.ErrorServiceSessionAlreadyOpen,
	servicesessionaction.ConflictReasonTransferTeamUnavailable:   i18n.ErrorTransferTeamUnavailable,
}

// serviceSessionMutationErrors 是服务周期命令的错误转换规则，未登记的冲突按不可回复提示。
var serviceSessionMutationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.FieldRule(conversationMessageValidationKeys),
	conversationConflictRule(i18n.ErrorServiceSessionNotReplyable, serviceSessionConflictKeys),
})

// serviceSessionMutationError 转换服务周期命令错误。
func serviceSessionMutationError(meta appservice.RequestMeta, err error) error {
	return serviceSessionMutationErrors.Translate(meta, err, i18n.ErrorServiceSessionUpdateFailed)
}

// serviceTextMessageErrors 是成员客户消息发送的错误转换规则。
var serviceTextMessageErrors = dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.Is(fileaction.ErrFileNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.FieldRule(conversationMessageValidationKeys),
	conversationConflictRule(i18n.ErrorMessageConflict, customerReplyConflictKeys),
}

// serviceTextMessageError 转换成员客户消息发送错误。
func serviceTextMessageError(meta appservice.RequestMeta, err error) error {
	return serviceTextMessageErrors.Translate(meta, err, i18n.ErrorMessageSendFailed)
}

// customerReplyConflictKeys 把对客回复资格冲突原因映射为本地化文案键，未登记的原因按消息冲突提示。
var customerReplyConflictKeys = map[string]i18n.Key{
	servicesessionaction.ConflictReasonServiceHandlingRequired:      i18n.ErrorServiceHandlingRequired,
	conversationaction.ConflictReasonServiceSessionOwned:            i18n.ErrorServiceSessionOwned,
	servicesessionaction.ConflictReasonServiceSessionOwnRequest:     i18n.ErrorServiceSessionOwnRequest,
	conversationaction.ConflictReasonServiceSessionNotReplyable:     i18n.ErrorServiceSessionNotReplyable,
	conversationaction.ConflictReasonChannelOutboundUnavailable:     i18n.ErrorChannelOutboundUnavailable,
	conversationaction.ConflictReasonChannelOutboundUnsupported:     i18n.ErrorChannelOutboundUnsupported,
	conversationaction.ConflictReasonChannelReplyWindowClosed:       i18n.ErrorChannelReplyWindowClosed,
	conversationaction.ConflictReasonChannelRecipientUnbound:        i18n.ErrorChannelRecipientUnbound,
	conversationaction.ConflictReasonReplyTargetInvalid:             i18n.ErrorReplyTargetInvalid,
	servicesessionaction.ConflictReasonChannelAttachmentUnsupported: i18n.ErrorChannelAttachmentUnsupported,
	conversationaction.ConflictReasonAttachmentTooLarge:             i18n.ErrorAttachmentTooLarge,
	servicesessionaction.ConflictReasonCaptionTooLong:               i18n.ErrorAttachmentCaptionTooLong,
	servicesessionaction.ConflictReasonTranslationTooLong:           i18n.ErrorTranslationTooLong,
	servicesessionaction.ConflictReasonTextTooLong:                  i18n.ErrorChannelTextTooLong,
	servicesessionaction.ConflictReasonNoteMentionTargetInvalid:     i18n.ErrorNoteMentionTargetInvalid,
}
