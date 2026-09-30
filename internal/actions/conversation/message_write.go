//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// MaxMessageBodyRunes 是消息正文的最大字符数。
const MaxMessageBodyRunes = 4000

// InternalTextMessageFields 是内部会话文本消息的基础字段，ReplyToMessageID 为空表示不引用。
type InternalTextMessageFields struct {
	ConversationID   string
	ClientMessageID  string
	Body             string
	ReplyToMessageID string
}

// NormalizeInternalTextMessageInput 规范化内部会话文本消息的基础字段与可选引用。
func NormalizeInternalTextMessageInput(input InternalTextMessageFields) (InternalTextMessageFields, map[string]ValidationCode) {
	fields := map[string]ValidationCode{}
	input.Body = strings.TrimSpace(input.Body)
	var valid bool
	input.ConversationID, valid = common.NormalizeUUID(input.ConversationID)
	if !valid {
		fields["conversationId"] = ValidationConversationIDInvalid
	}
	input.ClientMessageID, valid = common.NormalizeUUID(input.ClientMessageID)
	if !valid {
		fields["clientMessageId"] = ValidationClientMessageIDInvalid
	}
	if input.Body == "" {
		fields["body"] = ValidationBodyRequired
	} else if utf8.RuneCountInString(input.Body) > MaxMessageBodyRunes {
		fields["body"] = ValidationBodyTooLong
	}
	if input.ReplyToMessageID != "" {
		input.ReplyToMessageID, valid = common.NormalizeUUID(input.ReplyToMessageID)
		if !valid {
			fields["replyToMessageId"] = ValidationReplyToMessageIDInvalid
		}
	}
	return input, fields
}

// MaxWriteAttempts 是并发唯一约束冲突时的最大写入尝试次数。
const MaxWriteAttempts = 3

// RunInTxWithUniqueRetry 在实时通知事务中执行写入，遇到 constraintNames 中的并发唯一约束冲突时整体重试，最多执行 MaxWriteAttempts 次；fn 每次执行都须重新写入调用方的结果。
func RunInTxWithUniqueRetry(ctx context.Context, db *bun.DB, constraintNames map[string]struct{}, fn func(context.Context, bun.Tx) error) error {
	var err error
	for attempt := 1; attempt <= MaxWriteAttempts; attempt++ {
		if err = realtime.RunInTx(ctx, db, fn); err == nil {
			return nil
		}
		constraint, retryable := RetryableUniqueViolation(err, constraintNames)
		if !retryable {
			return err
		}
		slog.Info("并发唯一约束冲突，重试写入事务", "constraint", constraint, "attempt", attempt)
	}
	return fmt.Errorf("unique conflict retries exhausted: %w", err)
}

// RetryableUniqueViolation 返回允许重试的并发唯一约束。
func RetryableUniqueViolation(err error, constraintNames map[string]struct{}) (string, bool) {
	constraint, ok := pgerr.UniqueViolation(err)
	if !ok {
		return "", false
	}
	_, retryable := constraintNames[constraint]
	return constraint, retryable
}

type idempotentMemberMessageRow struct {
	ClientMessageID        *string                  `bun:"client_message_id"`
	ReplyToMessageID       *string                  `bun:"reply_to_message_id"`
	MessageSeq             int64                    `bun:"message_seq"`
	ID                     string                   `bun:"id"`
	CreatedAt              time.Time                `bun:"created_at"`
	ConversationID         string                   `bun:"conversation_id"`
	ServiceSessionID       *string                  `bun:"service_session_id"`
	SenderParticipantID    *string                  `bun:"sender_participant_id"`
	Type                   string                   `bun:"type"`
	Visibility             domain.MessageVisibility `bun:"visibility"`
	Body                   string                   `bun:"body"`
	Language               *string                  `bun:"language"`
	AuthoredBody           *string                  `bun:"authored_body"`
	AuthoredLanguage       *string                  `bun:"authored_language"`
	OriginatedAt           time.Time                `bun:"originated_at"`
	DeletedAt              *time.Time               `bun:"deleted_at"`
	SenderSubjectID        *string                  `bun:"sender_subject_id"`
	SenderSubjectKind      *string                  `bun:"sender_subject_kind"`
	SenderSubjectSourceID  *string                  `bun:"sender_subject_source_id"`
	JoinedServiceSessionID *string                  `bun:"joined_service_session_id"`
	AttachmentFileID       *string                  `bun:"attachment_file_id"`
	AttachmentName         *string                  `bun:"attachment_name"`
	AttachmentContentType  *string                  `bun:"attachment_content_type"`
	AttachmentByteSize     *int64                   `bun:"attachment_byte_size"`
	AttachmentImageWidth   *int                     `bun:"attachment_image_width"`
	AttachmentImageHeight  *int                     `bun:"attachment_image_height"`
	AttachmentTransfer     *string                  `bun:"attachment_transfer_status"`
}

// ServiceSessionExpectation 描述幂等核对对消息所属服务周期的要求。
type ServiceSessionExpectation int

const (
	// ServiceSessionAbsent 要求消息不属于服务周期。
	ServiceSessionAbsent ServiceSessionExpectation = iota
	// ServiceSessionPresent 要求消息属于会话中的服务周期。
	ServiceSessionPresent
	// ServiceSessionAssigned 接受发送时由服务端按 AI 员工服务对象分配的结果：不属于服务周期，或属于会话中的服务周期。
	ServiceSessionAssigned
)

// MemberMessageExpectation 定义幂等命中时必须完全一致的成员发送意图。
type MemberMessageExpectation struct {
	Attachment       *AttachmentExpectation
	ConversationID   string
	Body             string
	ReplyToMessageID string
	Type             domain.MessageType
	Visibility       domain.MessageVisibility
	ServiceSession   ServiceSessionExpectation
	// Translated 表示翻译发送，此时 Body 与保存的客服原话核对。
	Translated bool
}

// AttachmentExpectation 定义附件消息幂等核对所需的文件事实。
type AttachmentExpectation struct {
	FileID      string
	ImageWidth  int
	ImageHeight int
}

// InternalTextExpectation 构造内部会话文本消息的幂等核对意图，AI 聊天中的消息可能由服务端分配进服务周期。
func InternalTextExpectation(conversationID, body, replyToMessageID string) MemberMessageExpectation {
	return MemberMessageExpectation{
		ConversationID: conversationID, Body: body, ReplyToMessageID: replyToMessageID,
		Type: domain.MessageTypeText, Visibility: domain.MessageVisibilityShared, ServiceSession: ServiceSessionAssigned,
	}
}

// InternalAttachmentExpectation 构造内部会话附件消息的幂等核对意图，AI 聊天中的消息可能由服务端分配进服务周期。
func InternalAttachmentExpectation(conversationID, body string, attachment AttachmentExpectation) MemberMessageExpectation {
	return MemberMessageExpectation{
		ConversationID: conversationID, Body: body, Attachment: &attachment,
		Type: domain.MessageTypeAttachment, Visibility: domain.MessageVisibilityShared, ServiceSession: ServiceSessionAssigned,
	}
}

// LoadIdempotentMemberMessage 校验并返回已经保存的成员消息。
func LoadIdempotentMemberMessage(ctx context.Context, db bun.IDB, identity *servermodels.Identity, expectation MemberMessageExpectation, idempotencyKey string) (ConversationMessage, bool, error) {
	row := idempotentMemberMessageRow{}
	err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id AS id").
		ColumnExpr("msg.client_message_id").
		ColumnExpr("msg.created_at AS created_at").
		ColumnExpr("msg.conversation_id AS conversation_id").
		ColumnExpr("msg.service_session_id AS service_session_id").
		ColumnExpr("msg.sender_participant_id AS sender_participant_id").
		ColumnExpr("msg.type AS type").
		ColumnExpr("msg.visibility AS visibility").
		ColumnExpr("msg.body AS body").
		ColumnExpr("msg.language AS language").
		ColumnExpr("authored.body AS authored_body, authored.language AS authored_language").
		ColumnExpr("msg.reply_to_message_id AS reply_to_message_id").
		ColumnExpr("msg.originated_at AS originated_at").
		ColumnExpr("msg.deleted_at AS deleted_at").
		ColumnExpr("msg.message_seq AS message_seq").
		ColumnExpr("cs.id AS sender_subject_id").
		ColumnExpr("cs.kind AS sender_subject_kind").
		ColumnExpr("cs.source_id AS sender_subject_source_id").
		ColumnExpr("ss.id AS joined_service_session_id").
		ColumnExpr("ma.file_id::text AS attachment_file_id").
		ColumnExpr("ma.name AS attachment_name").
		ColumnExpr("ma.content_type AS attachment_content_type").
		ColumnExpr("ma.byte_size AS attachment_byte_size").
		ColumnExpr("ma.image_width AS attachment_image_width").
		ColumnExpr("ma.image_height AS attachment_image_height").
		ColumnExpr("ma.transfer_status AS attachment_transfer_status").
		Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.organization_id = msg.organization_id").
		Join("LEFT JOIN message_translations AS authored ON authored.message_id = msg.id AND authored.organization_id = msg.organization_id AND authored.authored").
		Join("LEFT JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = msg.service_session_id AND ss.organization_id = msg.organization_id AND ss.conversation_id = msg.conversation_id").
		Where("msg.organization_id = ?", identity.Organization.ID).
		Where("msg.idempotency_key = ?", idempotencyKey).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationMessage{}, false, nil
	}
	if err != nil {
		return ConversationMessage{}, false, fmt.Errorf("load idempotent member message: %w", err)
	}
	// 校验幂等消息对应完整的成员发送意图。
	storedReply := ""
	if row.ReplyToMessageID != nil {
		storedReply = *row.ReplyToMessageID
	}
	absent := row.ServiceSessionID == nil && row.JoinedServiceSessionID == nil
	present := row.ServiceSessionID != nil && row.JoinedServiceSessionID != nil && *row.ServiceSessionID == *row.JoinedServiceSessionID
	serviceSessionMatches := absent
	switch expectation.ServiceSession {
	case ServiceSessionPresent:
		serviceSessionMatches = present
	case ServiceSessionAssigned:
		serviceSessionMatches = absent || present
	}
	// 附件消息额外核对文件与图片尺寸，文本消息不得关联附件。
	attachmentMatches := row.AttachmentFileID == nil
	if expectation.Attachment != nil {
		attachmentMatches = row.AttachmentFileID != nil && *row.AttachmentFileID == expectation.Attachment.FileID &&
			row.AttachmentImageWidth != nil && *row.AttachmentImageWidth == expectation.Attachment.ImageWidth &&
			row.AttachmentImageHeight != nil && *row.AttachmentImageHeight == expectation.Attachment.ImageHeight
	}
	// 翻译发送核对客服书写的原话，译文每次生成可能不同。
	bodyMatches := row.Body == expectation.Body && row.AuthoredBody == nil
	if expectation.Translated {
		bodyMatches = row.AuthoredBody != nil && *row.AuthoredBody == expectation.Body
	}
	messageMatches := storedReply == expectation.ReplyToMessageID && row.ConversationID == expectation.ConversationID && bodyMatches &&
		row.Type == string(expectation.Type) && row.Visibility == expectation.Visibility && row.DeletedAt == nil &&
		serviceSessionMatches && attachmentMatches &&
		row.SenderParticipantID != nil && row.SenderSubjectID != nil && row.SenderSubjectKind != nil && row.SenderSubjectSourceID != nil &&
		*row.SenderSubjectKind == string(domain.ChatSubjectKindOrganizationIdentity) && *row.SenderSubjectSourceID == identity.OrganizationIdentity.ID
	if !messageMatches {
		return ConversationMessage{}, true, &ConflictError{Reason: ConflictReasonIdempotencyMismatch}
	}
	message := &servermodels.Message{
		ClientMessageID: row.ClientMessageID, ID: row.ID, CreatedAt: row.CreatedAt, ConversationID: row.ConversationID,
		ServiceSessionID: row.ServiceSessionID, SenderParticipantID: row.SenderParticipantID,
		Type: row.Type, Visibility: string(row.Visibility), Body: row.Body, Language: row.Language, OriginatedAt: row.OriginatedAt, DeletedAt: row.DeletedAt, MessageSeq: row.MessageSeq,
	}
	result := MemberConversationMessage(message, *row.SenderSubjectID, identity.OrganizationIdentity)
	if row.AuthoredBody != nil && row.AuthoredLanguage != nil {
		result.Translation = &MessageTranslation{Language: *row.AuthoredLanguage, Body: *row.AuthoredBody}
	}
	// 附件行存在时其余列均非空。
	if row.AttachmentFileID != nil {
		result.Attachment = &MessageAttachment{
			ID: *row.AttachmentFileID, Name: *row.AttachmentName, ContentType: *row.AttachmentContentType,
			ByteSize: *row.AttachmentByteSize, ImageWidth: *row.AttachmentImageWidth, ImageHeight: *row.AttachmentImageHeight,
			TransferStatus: domain.MessageAttachmentTransferStatus(*row.AttachmentTransfer),
		}
	}
	if storedReply != "" {
		result.ReplyTo, err = LoadMessageReference(ctx, db, identity.Organization.ID, expectation.ConversationID, storedReply)
		if err != nil {
			return ConversationMessage{}, true, fmt.Errorf("load idempotent message reference: %w", err)
		}
	}
	return result, true, nil
}

// MemberConversationMessage 构造成员消息时间线结果。
func MemberConversationMessage(message *servermodels.Message, subjectID string, identity servermodels.OrganizationIdentity) ConversationMessage {
	name := identity.DisplayName
	identityType := domain.OrganizationIdentityType(identity.Type)
	return ConversationMessage{
		ClientMessageID: message.ClientMessageID, ID: message.ID, Type: domain.MessageType(message.Type), Visibility: domain.MessageVisibility(message.Visibility), Body: message.Body, Language: message.Language,
		OriginatedAt: message.OriginatedAt, SourceOrder: message.SourceOrder, CreatedAt: message.CreatedAt, MentionAll: message.MentionAll, MessageSeq: message.MessageSeq,
		Sender: &ConversationMessageSender{
			ChatSubjectID: subjectID, Kind: domain.ChatSubjectKindOrganizationIdentity,
			SourceID: identity.ID, DisplayName: &name, AvatarFileID: identity.AvatarFileID, IdentityType: &identityType,
		},
	}
}
