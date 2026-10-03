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
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ListServiceCopilotThreads 返回服务会话的全部 Copilot 线程。
func (o *directOperations) ListServiceCopilotThreads(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceCopilotThreadList, error) {
	threads, err := o.listServiceCopilotThreads.Execute(ctx, identity, conversationID)
	if err != nil {
		if errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceCopilotThreadList{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		return appservice.ServiceCopilotThreadList{}, appservice.FailedError(meta, i18n.ErrorServiceCopilotThreadListFailed, err)
	}
	fileIDs := make([]*string, 0, len(threads))
	for _, thread := range threads {
		fileIDs = append(fileIDs, thread.AgentAvatarFileID)
	}
	urls, err := o.conversationAvatarURLs(ctx, identity, nil, fileIDs...)
	if err != nil {
		slog.Warn("读取 Copilot 线程 AI 员工头像失败", "conversation_id", conversationID, "error", err)
	}
	output := make([]appservice.ServiceCopilotThread, 0, len(threads))
	for _, thread := range threads {
		output = append(output, serviceCopilotThreadFromAction(thread, urls))
	}
	return appservice.ServiceCopilotThreadList{Threads: output}, nil
}

// SendFirstServiceCopilotMessage 保存首条提问并确认新建的 Copilot 线程。
func (o *directOperations) SendFirstServiceCopilotMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.FirstServiceCopilotMessageInput) (appservice.FirstServiceCopilotMessageResult, error) {
	result, err := o.sendFirstServiceCopilotMessage.Execute(ctx, identity, directchataction.FirstServiceCopilotMessageInput{
		ThreadID: input.ThreadID, ServedConversationID: conversationID, AgentIdentityID: input.AgentIdentityID,
		ClientMessageID: input.ClientMessageID, Body: input.Body,
	})
	if err != nil {
		return appservice.FirstServiceCopilotMessageResult{}, individualConversationError(meta, err, "start_copilot")
	}
	slog.Info("Copilot 线程首条提问已保存", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "thread_id", result.Thread.ID, "message_id", result.Message.ID, "agent_identity_id", result.Thread.AgentIdentityID)
	urls, err := o.conversationAvatarURLs(ctx, identity, []conversationaction.ConversationMessage{result.Message}, result.Thread.AgentAvatarFileID)
	if err != nil {
		slog.Warn("读取 Copilot 线程头像失败", "thread_id", result.Thread.ID, "error", err)
	}
	return appservice.FirstServiceCopilotMessageResult{
		Thread:  serviceCopilotThreadFromAction(result.Thread, urls),
		Message: conversationMessageFromAction(result.Message, urls),
	}, nil
}

// SendServiceCopilotTextMessage 保存当前成员在 Copilot 线程中的提问。
func (o *directOperations) SendServiceCopilotTextMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, threadID string, input appservice.ServiceCopilotTextMessageInput) (appservice.ConversationMessage, error) {
	message, err := o.sendServiceCopilotTextMessage.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: threadID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID})
	if err != nil {
		return appservice.ConversationMessage{}, individualConversationError(meta, err, "send_copilot")
	}
	slog.Info("Copilot 线程提问已保存", "organization_id", identity.Organization.ID, "thread_id", threadID, "message_id", message.ID)
	return o.conversationMessageWithAvatar(ctx, identity, message), nil
}

// StopServiceCopilotReply 停止 Copilot 线程中指定的回复。
func (o *directOperations) StopServiceCopilotReply(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, threadID, runID string) (appservice.AgentRunStatus, error) {
	status, err := o.agentCoordinator.StopServiceCopilotReply(ctx, identity, threadID, runID)
	if err == nil {
		return appservice.AgentRunStatus(status), nil
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return "", appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return "", appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	return "", appservice.FailedError(meta, i18n.ErrorAgentReplyStopFailed, err)
}

// serviceCopilotThreadFromAction 转换线程摘要并补充 AI 员工头像地址。
func serviceCopilotThreadFromAction(thread directchataction.ServiceCopilotThread, avatarURLs map[string]string) appservice.ServiceCopilotThread {
	output := appservice.ServiceCopilotThread{
		ID: thread.ID, Title: thread.Title, AgentIdentityID: thread.AgentIdentityID, AgentName: thread.AgentName,
		AgentActive: thread.AgentStatus == domain.IdentityStatusActive, CreatedByIdentityID: thread.CreatedByIdentityID,
		CreatedByName: thread.CreatedByName, CreatedAt: thread.CreatedAt, LastActivityAt: thread.LastActivityAt,
	}
	if thread.AgentAvatarFileID != nil {
		output.AgentAvatarURL = avatarURLs[*thread.AgentAvatarFileID]
	}
	return output
}
