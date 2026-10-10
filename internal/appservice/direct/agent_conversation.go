//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// StopAgentReply 停止指定独立 AI 会话的回复。
func (o *agentOps) StopAgentReply(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, runID string) (appservice.AgentRunStatus, error) {
	status, err := o.runCancellation.StopAgentReply(ctx, identity, conversationID, runID)
	if err == nil {
		return status, nil
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return "", appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return "", appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	return "", appservice.FailedError(meta, i18n.ErrorAgentReplyStopFailed, err)
}

// StopGroupAgentReply 停止群聊中指定 AI 员工的回复。
func (o *agentOps) StopGroupAgentReply(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, runID string) (appservice.AgentRunStatus, error) {
	status, err := o.runCancellation.StopGroupAgentReply(ctx, identity, conversationID, runID)
	if err == nil {
		return status, nil
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return "", appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return "", appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	return "", appservice.FailedError(meta, i18n.ErrorAgentReplyStopFailed, err)
}

// SendFirstAgentTextMessage 保存 AI 聊天首条消息并确认草稿对应的会话。
func (o *conversationOps) SendFirstAgentTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.FirstAgentTextMessageInput) (appservice.FirstAgentTextMessageResult, error) {
	result, err := o.sendFirstAgentTextMessage.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: input.ConversationID, AgentIdentityID: input.AgentIdentityID, ClientMessageID: input.ClientMessageID, Body: input.Body})
	if err != nil {
		return appservice.FirstAgentTextMessageResult{}, individualConversationError(meta, err, "start_agent")
	}
	slog.InfoContext(ctx, "AI 聊天首条消息已保存", "conversation_id", result.Conversation.ID, "message_id", result.Message.ID, "agent_identity_id", input.AgentIdentityID)
	avatarURLs, err := o.conversationAvatarURLs(ctx, identity, []conversationaction.ConversationMessage{result.Message}, result.Conversation.Agent.AgentAvatarFileID)
	if err != nil {
		slog.WarnContext(ctx, "读取 AI 聊天头像失败", "conversation_id", input.ConversationID, "error", err)
	}
	summary := result.Conversation
	return appservice.FirstAgentTextMessageResult{
		Conversation: inboxConversationFromAction(summary, avatarURLs),
		Message:      conversationMessageFromAction(ctx, result.Message, avatarURLs),
	}, nil
}

// SendAgentTextMessage 保存当前成员在指定 AI 会话中的消息。
func (o *conversationOps) SendAgentTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.AgentTextMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendAgentTextMessage.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID})
	if err != nil {
		return appservice.ConversationMessage{}, individualConversationError(meta, err, "send_agent")
	}
	slog.InfoContext(ctx, "AI 聊天成员消息已保存", "conversation_id", conversationID, "message_id", message.ID)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}
