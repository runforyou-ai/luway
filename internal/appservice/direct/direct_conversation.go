//go:build server

// 真人单聊操作与双方聊天错误转换。

package direct

import (
	"context"
	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/mapx"
)

// SendFirstDirectTextMessage 发送首条单聊消息并按需创建长期会话。
func (o *conversationOps) SendFirstDirectTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.FirstDirectTextMessageInput) (appservice.FirstDirectTextMessageResult, error) {
	result, err := o.sendFirstDirectTextMessage.Execute(ctx, identity, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: input.TargetIdentityID, ClientMessageID: input.ClientMessageID, Body: input.Body,
	})
	if err != nil {
		return appservice.FirstDirectTextMessageResult{}, individualConversationError(meta, err, "send_first")
	}
	slog.InfoContext(ctx, "企业成员内部单聊首条文本消息已保存",
		"conversation_id", result.Conversation.ID,
		"target_identity_id", result.Conversation.PeerIdentityID,
		"message_id", result.Message.ID,
	)
	avatarURLs, err := o.conversationAvatarURLs(ctx, identity, []conversationaction.ConversationMessage{result.Message}, result.Conversation.PeerAvatarFileID)
	if err != nil {
		slog.WarnContext(ctx, "读取已保存单聊首条消息头像失败", "conversation_id", result.Conversation.ID, "message_id", result.Message.ID, "error", err)
	}
	return appservice.FirstDirectTextMessageResult{
		Conversation: directInboxConversationFromSummary(result.Conversation, avatarURLs),
		Message:      conversationMessageFromAction(ctx, result.Message, avatarURLs),
	}, nil
}

// FindDirectConversation 按目标身份查找当前成员的活跃单聊。
func (o *conversationOps) FindDirectConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, targetIdentityID string) (appservice.DirectConversationLookup, error) {
	summary, err := o.findDirectConversation.Execute(ctx, identity, targetIdentityID)
	if err != nil {
		return appservice.DirectConversationLookup{}, individualConversationError(meta, err, "find")
	}
	if summary == nil {
		return appservice.DirectConversationLookup{}, nil
	}
	avatarURLs, err := o.conversationAvatarURLs(ctx, identity, nil, summary.PeerAvatarFileID)
	if err != nil {
		return appservice.DirectConversationLookup{}, individualConversationError(meta, err, "find")
	}
	conversation := directInboxConversationFromSummary(*summary, avatarURLs)
	return appservice.DirectConversationLookup{Conversation: &conversation}, nil
}

// directInboxConversationFromSummary 把单聊摘要转换为统一收件箱会话。
func directInboxConversationFromSummary(summary directchataction.DirectConversationSummary, avatarURLs map[string]string) appservice.InboxConversation {
	return appservice.InboxConversation{
		ID: summary.ID, Type: domain.ConversationTypeDirect, LastActivityAt: summary.LastActivityAt,
		Direct: &appservice.DirectInboxConversation{
			PeerIdentityID: summary.PeerIdentityID, PeerType: summary.PeerType, PeerName: summary.PeerName, PeerAvatarURL: optionalFileURL(avatarURLs, summary.PeerAvatarFileID),
			Preview: messagePreviewText(summary.Preview, summary.PreviewSenderIdentityType), LastMessageAt: summary.LastMessageAt,
		},
	}
}

// SendDirectTextMessage 发送内部单聊文本消息。
func (o *conversationOps) SendDirectTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.DirectTextMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendDirectTextMessage.Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
	})
	if err != nil {
		return appservice.ConversationMessage{}, individualConversationError(meta, err, "send")
	}
	slog.InfoContext(ctx, "企业成员内部单聊文本消息已保存",
		"conversation_id", conversationID,
		"message_id", message.ID,
		"sender_identity_id", identity.WorkspaceIdentity.ID,
	)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// individualConversationConflictKeys 把真人单聊和 AI 聊天的冲突原因映射为本地化文案键。
var individualConversationConflictKeys = mapx.Merge(
	personalAgentConflictKeys,
	map[string]i18n.Key{conversationaction.ConflictReasonReplyTargetInvalid: i18n.ErrorReplyTargetInvalid},
)

// individualConversationErrors 是真人单聊和 AI 聊天的目标及消息错误转换规则。
var individualConversationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrAgentTargetNotFound, dispatch.NotFound(i18n.ErrorAgentNotFound)),
	dispatch.Is(conversationaction.ErrAgentUnavailable, dispatch.NotFound(i18n.ErrorAgentUnavailable)),
	dispatch.Is(conversationaction.ErrDirectTargetNotFound, dispatch.NotFound(i18n.ErrorDirectTargetNotFound)),
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.FieldRule(conversationMessageValidationKeys),
	conversationConflictRule(i18n.ErrorMessageConflict, individualConversationConflictKeys),
})

// individualConversationError 转换真人单聊和 AI 聊天的目标及消息错误。
func individualConversationError(meta appservice.RequestMeta, err error, operation string) error {
	failureKey := i18n.ErrorDirectMessageSendFailed
	if operation == "find" {
		failureKey = i18n.ErrorDirectConversationLookupFailed
	}
	return individualConversationErrors.Translate(meta, err, failureKey)
}
