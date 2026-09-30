//go:build server

package servicesession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
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

// Execute 在一个可重试事务中写入成员客户会话附件回复并激活文件。
func (a *SendServiceAttachmentMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input ServiceAttachmentMessageInput) (conversationaction.ConversationMessage, error) {
	normalized, fields := normalizeServiceAttachmentMessageInput(input)
	if len(fields) > 0 {
		return conversationaction.ConversationMessage{}, &conversationaction.ValidationError{Fields: fields}
	}
	// 预生成一次事务重试期间稳定使用的 UUIDv7。
	values := make([]string, 3)
	for index := range values {
		values[index] = uuid.NewV7().String()
	}
	ids := memberMessageIDs{subject: values[0], participant: values[1], message: values[2]}
	idempotencyKey := "mmsg:" + identity.OrganizationIdentity.ID + ":" + normalized.ClientMessageID
	payload := customerMessagePayload{
		ConversationID: normalized.ConversationID, ClientMessageID: normalized.ClientMessageID,
		Body: normalized.Body, ReplyToMessageID: normalized.ReplyToMessageID, Type: domain.MessageTypeAttachment,
		// 附件只用于对客回复，内部备注附件不在本次范围内。
		Visibility: domain.MessageVisibilityShared,
		Attachment: &customerAttachmentPayload{FileID: normalized.FileID, ImageWidth: normalized.ImageWidth, ImageHeight: normalized.ImageHeight},
	}
	var result conversationaction.ConversationMessage
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, memberMessageRetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		var executeErr error
		result, executeErr = sendCustomerMessage(ctx, tx, identity, a.enqueuer, payload, ids, idempotencyKey)
		return executeErr
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 发送结果与历史查询使用同一引用能力判定，未取得平台回执时不可被引用。
	replyUnavailable, err := conversationaction.MessageReplyUnavailable(ctx, a.db, identity, normalized.ConversationID, result.ID)
	if err != nil {
		return conversationaction.ConversationMessage{}, fmt.Errorf("load sent attachment reference state: %w", err)
	}
	result.ReplyUnavailable = replyUnavailable
	return result, nil
}

// normalizeServiceAttachmentMessageInput 规范化并校验成员服务会话附件输入。
func normalizeServiceAttachmentMessageInput(input ServiceAttachmentMessageInput) (ServiceAttachmentMessageInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	input.Body = strings.TrimSpace(input.Body)
	var valid bool
	input.ConversationID, valid = common.NormalizeUUID(input.ConversationID)
	if !valid {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	input.ClientMessageID, valid = common.NormalizeUUID(input.ClientMessageID)
	if !valid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	input.FileID, valid = common.NormalizeUUID(input.FileID)
	if !valid {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	if input.ReplyToMessageID != "" {
		input.ReplyToMessageID, valid = common.NormalizeUUID(input.ReplyToMessageID)
		if !valid {
			fields["replyToMessageId"] = conversationaction.ValidationReplyToMessageIDInvalid
		}
	}
	if utf8.RuneCountInString(input.Body) > conversationaction.MaxMessageBodyRunes {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	if input.ImageWidth < 0 || input.ImageHeight < 0 {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	return input, fields
}

// lockCustomerAttachmentFile 锁定本人上传的有效文件并按渠道上限校验字节数。
func lockCustomerAttachmentFile(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, channelType domain.ChannelType, payload customerAttachmentPayload) (*conversationaction.MessageAttachment, error) {
	file := &servermodels.File{}
	err := tx.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.organization_id = ? AND f.created_by_user_id = ?", payload.FileID, identity.Organization.ID, identity.User.ID).
		Where("f.purpose = ? AND f.uploader_channel_identity_id IS NULL", domain.FilePurposeMessageAttachment).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	if err != nil {
		return nil, err
	}
	if file.Status != string(domain.FileStatusUploaded) || file.Expired {
		return nil, fileaction.ErrFileNotFound
	}
	if limit := domain.ChannelAttachmentLimit(channelType); limit > 0 && file.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	if _, err := tx.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate customer attachment file: %w", err)
	}
	return &conversationaction.MessageAttachment{
		ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize,
		ImageWidth: payload.ImageWidth, ImageHeight: payload.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady,
	}, nil
}
