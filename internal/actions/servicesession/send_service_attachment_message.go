//go:build server

package servicesession

import (
	"context"
	"strings"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/membersend"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// SendServiceAttachmentMessageAction 持久化企业成员发往服务会话的附件消息。
type SendServiceAttachmentMessageAction struct {
	enqueuer servertask.TxEnqueuer
	db       *bun.DB
}

// NewSendServiceAttachmentMessageAction 创建成员服务会话附件发送操作。
func NewSendServiceAttachmentMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *SendServiceAttachmentMessageAction {
	return &SendServiceAttachmentMessageAction{db: db, enqueuer: enqueuer}
}

// Execute 在一个事务中写入成员客户会话附件回复并激活文件。
func (a *SendServiceAttachmentMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input ServiceAttachmentMessageInput) (conversationaction.ConversationMessage, error) {
	normalized := normalizeServiceAttachmentMessageInput(input)
	return runCustomerMessage(ctx, a.db, a.enqueuer, identity, customerMessagePayload{
		ConversationID: normalized.ConversationID, ClientMessageID: normalized.ClientMessageID,
		Body: normalized.Body, ReplyToMessageID: normalized.ReplyToMessageID, Type: domain.MessageTypeAttachment,
		// 附件消息只用于对客回复。
		Visibility: domain.MessageVisibilityShared,
		Attachment: &membersend.AttachmentIntent{FileID: normalized.FileID, ImageWidth: normalized.ImageWidth, ImageHeight: normalized.ImageHeight},
	}, "attachment")
}

// normalizeServiceAttachmentMessageInput 规范化成员服务会话附件输入。
func normalizeServiceAttachmentMessageInput(input ServiceAttachmentMessageInput) ServiceAttachmentMessageInput {
	input.Body = strings.TrimSpace(input.Body)
	input.ConversationID, _ = str.NormalizeUUID(input.ConversationID)
	input.ClientMessageID, _ = str.NormalizeUUID(input.ClientMessageID)
	input.FileID, _ = str.NormalizeUUID(input.FileID)
	if input.ReplyToMessageID != "" {
		input.ReplyToMessageID, _ = str.NormalizeUUID(input.ReplyToMessageID)
	}
	return input
}

// activateCustomerAttachment 激活本人上传的附件文件；channel 不为空时按来源渠道接受的附件类型与字节上限校验。
func activateCustomerAttachment(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, channel *domain.ChannelCapabilities, intent membersend.AttachmentIntent) (*conversationaction.MessageAttachment, error) {
	attachment, err := membersend.ActivateAttachment(ctx, tx, identity, intent)
	if err != nil || channel == nil {
		return attachment, err
	}
	limit, accepted := channel.AttachmentByteLimit(attachment.ContentType)
	if !accepted {
		return nil, &conversationaction.ConflictError{Reason: ConflictReasonChannelAttachmentUnsupported}
	}
	if attachment.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	return attachment, nil
}
