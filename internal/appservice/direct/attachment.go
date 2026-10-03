//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/url"

	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// SendAttachmentMessage 保存已上传的内部会话附件，并返回消息及首发创建的单聊或 AI 聊天。
func (o *directOperations) SendAttachmentMessage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AttachmentMessageInput) (appservice.AttachmentMessageResult, error) {
	result, err := o.sendAttachmentMessage.Execute(ctx, identity, directchataction.AttachmentMessageInput{
		ConversationID: input.ConversationID, TargetIdentityID: input.TargetIdentityID, AgentIdentityID: input.AgentIdentityID, ServedConversationID: input.ServedConversationID,
		ClientMessageID: input.ClientMessageID, FileID: input.FileID, Body: input.Body, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight,
	})
	if errors.Is(err, fileaction.ErrFileNotFound) {
		return appservice.AttachmentMessageResult{}, appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	if err != nil {
		return appservice.AttachmentMessageResult{}, individualConversationError(meta, err, "send_attachment")
	}
	output := appservice.AttachmentMessageResult{ConversationID: result.ConversationID, Message: o.conversationMessageWithAvatar(ctx, identity, result.Message)}
	if result.Conversation != nil {
		urls, err := o.conversationAvatarURLs(ctx, identity, nil, result.Conversation.PeerAvatarFileID)
		if err != nil {
			slog.Warn("读取附件首发单聊头像失败", "conversation_id", result.ConversationID, "error", err)
		}
		conversation := directInboxConversationFromSummary(*result.Conversation, urls)
		output.Conversation = &conversation
	}
	if result.AgentConversation != nil {
		urls, err := o.conversationAvatarURLs(ctx, identity, nil, result.AgentConversation.Agent.AgentAvatarFileID)
		if err != nil {
			slog.Warn("读取附件首发 AI 聊天头像失败", "conversation_id", result.ConversationID, "error", err)
		}
		conversation := inboxConversationFromAction(*result.AgentConversation, urls)
		output.Conversation = &conversation
	}
	slog.Info("聊天附件消息已保存", "organization_id", identity.Organization.ID, "conversation_id", result.ConversationID, "message_id", result.Message.ID, "file_id", input.FileID)
	return output, nil
}

// GetAttachmentDownload 按文件实际存储位置创建带原始文件名的下载请求，可内嵌展示的图片同时返回预览地址。
func (o *directOperations) GetAttachmentDownload(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, messageID string) (appservice.FileDownload, error) {
	record, err := o.listConversationMessages.GetAttachmentFile(ctx, identity, conversationID, messageID)
	if err != nil {
		return appservice.FileDownload{}, o.fileOperationError(meta, err, i18n.ErrorFileNotFound)
	}
	_, inline := domain.InlineImageExtension(record.ContentType)
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		contentURL, err := o.links.URL(domain.FileStorageBackendLocal, record.StorageKey)
		if err != nil {
			return appservice.FileDownload{}, o.fileOperationError(meta, err, i18n.ErrorFileNotFound)
		}
		download := appservice.FileDownload{URL: contentURL + "?download=" + url.QueryEscape(record.OriginalName)}
		if inline {
			download.PreviewURL = contentURL
		}
		return download, nil
	}
	request, err := serverfilecontent.PresignDownload(ctx, o.s3, record.StorageKey, mime.FormatMediaType("attachment", map[string]string{"filename": record.OriginalName}))
	if err != nil {
		return appservice.FileDownload{}, o.fileOperationError(meta, err, i18n.ErrorFileNotFound)
	}
	download := appservice.FileDownload{URL: request.URL}
	if inline {
		preview, err := serverfilecontent.PresignDownload(ctx, o.s3, record.StorageKey, "inline")
		if err != nil {
			return appservice.FileDownload{}, o.fileOperationError(meta, err, i18n.ErrorFileNotFound)
		}
		download.PreviewURL = preview.URL
	}
	return download, nil
}
