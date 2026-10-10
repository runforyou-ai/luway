//go:build server

// Package membersend 提供成员在各类会话中发送消息共用的阶段：幂等键与重放核对、消息构造、附件文件激活和发送者阅读水位推进；会话锁定与发送资格由各会话类型自行校验。
package membersend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// Key 返回成员以该发送编号发送消息的幂等键。
func Key(identity *servermodels.Identity, clientMessageID string) string {
	return "mmsg:" + identity.WorkspaceIdentity.ID + ":" + clientMessageID
}

// ServiceSessionIntent 描述幂等核对对消息所属服务周期的要求。
type ServiceSessionIntent int

const (
	// ServiceSessionAbsent 要求消息不属于服务周期。
	ServiceSessionAbsent ServiceSessionIntent = iota
	// ServiceSessionPresent 要求消息属于会话中的服务周期。
	ServiceSessionPresent
	// ServiceSessionAssigned 接受发送时由服务端按 AI 员工服务对象分配的结果：不属于服务周期，或属于会话中的服务周期。
	ServiceSessionAssigned
)

// Intent 定义幂等命中时必须完全一致的成员发送意图。
type Intent struct {
	Attachment       *AttachmentIntent
	ConversationID   string
	Body             string
	ReplyToMessageID string
	Type             domain.MessageType
	Visibility       domain.MessageVisibility
	ServiceSession   ServiceSessionIntent
	// Translated 表示翻译发送，此时 Body 与保存的客服原话核对。
	Translated bool
	MentionAll bool
	// MentionSubjectIDs 是群聊提醒的聊天主体编号，为 nil 时不核对。
	MentionSubjectIDs []string
	// MentionIdentityIDs 是内部备注提醒的企业成员身份编号，为 nil 时不核对。
	MentionIdentityIDs []string
}

// AttachmentIntent 定义附件消息幂等核对所需的文件事实。
type AttachmentIntent struct {
	FileID      string
	ImageWidth  int
	ImageHeight int
}

// InternalText 构造内部会话文本消息的发送意图，AI 聊天中的消息可能由服务端分配进服务周期。
func InternalText(conversationID, body, replyToMessageID string) Intent {
	return Intent{
		ConversationID: conversationID, Body: body, ReplyToMessageID: replyToMessageID,
		Type: domain.MessageTypeText, Visibility: domain.MessageVisibilityShared, ServiceSession: ServiceSessionAssigned,
	}
}

// InternalAttachment 构造内部会话附件消息的发送意图，AI 聊天中的消息可能由服务端分配进服务周期。
func InternalAttachment(conversationID, body string, attachment AttachmentIntent) Intent {
	return Intent{
		ConversationID: conversationID, Body: body, Attachment: &attachment,
		Type: domain.MessageTypeAttachment, Visibility: domain.MessageVisibilityShared, ServiceSession: ServiceSessionAssigned,
	}
}

// savedMessageRow 是按幂等键读取的已保存成员消息及其发送者、服务周期、原话与附件。
type savedMessageRow struct {
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
	MentionAll             bool                     `bun:"mention_all"`
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

// Replay 按幂等键读取本人已保存的消息并核对完整发送意图，未保存时 found 为 false，意图不一致时返回幂等冲突。
func Replay(ctx context.Context, db bun.IDB, identity *servermodels.Identity, intent Intent, key string) (result conversationaction.ConversationMessage, found bool, err error) {
	row := savedMessageRow{}
	err = db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.client_message_id, msg.created_at, msg.conversation_id, msg.service_session_id, msg.sender_participant_id").
		ColumnExpr("msg.type, msg.visibility, msg.body, msg.language, msg.mention_all, msg.reply_to_message_id, msg.originated_at, msg.deleted_at, msg.message_seq").
		ColumnExpr("authored.body AS authored_body, authored.language AS authored_language").
		ColumnExpr("cs.id AS sender_subject_id, cs.kind AS sender_subject_kind, cs.source_id AS sender_subject_source_id").
		ColumnExpr("ss.id AS joined_service_session_id").
		ColumnExpr("ma.file_id::text AS attachment_file_id, ma.name AS attachment_name, ma.content_type AS attachment_content_type, ma.byte_size AS attachment_byte_size").
		ColumnExpr("ma.image_width AS attachment_image_width, ma.image_height AS attachment_image_height, ma.transfer_status AS attachment_transfer_status").
		Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.workspace_id = msg.workspace_id").
		Join("LEFT JOIN message_translations AS authored ON authored.message_id = msg.id AND authored.workspace_id = msg.workspace_id AND authored.authored").
		Join("LEFT JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = msg.service_session_id AND ss.workspace_id = msg.workspace_id AND ss.conversation_id = msg.conversation_id").
		Where("msg.workspace_id = ?", identity.Workspace.ID).
		Where("msg.idempotency_key = ?", key).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationaction.ConversationMessage{}, false, nil
	}
	if err != nil {
		return conversationaction.ConversationMessage{}, false, fmt.Errorf("load idempotent member message: %w", err)
	}
	mismatch := &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	if !row.matches(identity, intent) {
		return conversationaction.ConversationMessage{}, true, mismatch
	}
	message := &servermodels.Message{
		ClientMessageID: row.ClientMessageID, ID: row.ID, CreatedAt: row.CreatedAt, ConversationID: row.ConversationID,
		ServiceSessionID: row.ServiceSessionID, SenderParticipantID: row.SenderParticipantID, MentionAll: row.MentionAll,
		Type: row.Type, Visibility: string(row.Visibility), Body: row.Body, Language: row.Language, OriginatedAt: row.OriginatedAt, DeletedAt: row.DeletedAt, MessageSeq: row.MessageSeq,
	}
	result = conversationaction.MemberConversationMessage(message, *row.SenderSubjectID, identity.WorkspaceIdentity)
	if row.AuthoredBody != nil && row.AuthoredLanguage != nil {
		result.Translation = &conversationaction.MessageTranslation{Language: *row.AuthoredLanguage, Body: *row.AuthoredBody}
	}
	// 附件行存在时其余列均非空。
	if row.AttachmentFileID != nil {
		result.Attachment = &conversationaction.MessageAttachment{
			ID: *row.AttachmentFileID, Name: *row.AttachmentName, ContentType: *row.AttachmentContentType,
			ByteSize: *row.AttachmentByteSize, ImageWidth: *row.AttachmentImageWidth, ImageHeight: *row.AttachmentImageHeight,
			TransferStatus: domain.MessageAttachmentTransferStatus(*row.AttachmentTransfer),
		}
	}
	if row.ReplyToMessageID != nil {
		if result.ReplyTo, err = conversationaction.LoadMessageReference(ctx, db, identity.Workspace.ID, row.ConversationID, *row.ReplyToMessageID); err != nil {
			return conversationaction.ConversationMessage{}, true, fmt.Errorf("load idempotent message reference: %w", err)
		}
	}
	if result.Mentions, err = conversationaction.LoadPersistedMessageMentions(ctx, db, identity.Workspace.ID, row.ID); err != nil {
		return conversationaction.ConversationMessage{}, true, err
	}
	// 提醒对象按集合核对。
	subjectIDs := arr.Map(result.Mentions, func(mention conversationaction.ConversationMessageMention) string { return mention.ChatSubjectID })
	identityIDs := arr.Map(result.Mentions, func(mention conversationaction.ConversationMessageMention) string { return mention.SourceID })
	if !sameSet(intent.MentionSubjectIDs, subjectIDs) || !sameSet(intent.MentionIdentityIDs, identityIDs) {
		return conversationaction.ConversationMessage{}, true, mismatch
	}
	return result, true, nil
}

// matches 判断已保存消息与发送意图除提醒对象外的字段是否一致，且由本人发送。
func (row savedMessageRow) matches(identity *servermodels.Identity, intent Intent) bool {
	storedReply := support.Deref(row.ReplyToMessageID)
	absent := row.ServiceSessionID == nil && row.JoinedServiceSessionID == nil
	present := row.ServiceSessionID != nil && row.JoinedServiceSessionID != nil && *row.ServiceSessionID == *row.JoinedServiceSessionID
	serviceSessionMatches := absent
	switch intent.ServiceSession {
	case ServiceSessionPresent:
		serviceSessionMatches = present
	case ServiceSessionAssigned:
		serviceSessionMatches = absent || present
	}
	// 附件消息额外核对文件与图片尺寸，文本消息不得关联附件。
	attachmentMatches := row.AttachmentFileID == nil
	if intent.Attachment != nil {
		attachmentMatches = row.AttachmentFileID != nil && *row.AttachmentFileID == intent.Attachment.FileID &&
			row.AttachmentImageWidth != nil && *row.AttachmentImageWidth == intent.Attachment.ImageWidth &&
			row.AttachmentImageHeight != nil && *row.AttachmentImageHeight == intent.Attachment.ImageHeight
	}
	// 翻译发送核对客服书写的原话，译文每次生成可能不同。
	bodyMatches := row.Body == intent.Body && row.AuthoredBody == nil
	if intent.Translated {
		bodyMatches = row.AuthoredBody != nil && *row.AuthoredBody == intent.Body
	}
	return storedReply == intent.ReplyToMessageID && row.ConversationID == intent.ConversationID && bodyMatches &&
		row.Type == string(intent.Type) && row.Visibility == intent.Visibility && row.MentionAll == intent.MentionAll && row.DeletedAt == nil &&
		serviceSessionMatches && attachmentMatches &&
		row.SenderParticipantID != nil && row.SenderSubjectID != nil && row.SenderSubjectKind != nil && row.SenderSubjectSourceID != nil &&
		*row.SenderSubjectKind == string(domain.ChatSubjectKindWorkspaceIdentity) && *row.SenderSubjectSourceID == identity.WorkspaceIdentity.ID
}

// sameSet 判断两组编号作为集合是否相同，expected 为 nil 时视为相同。
func sameSet(expected, stored []string) bool {
	if expected == nil {
		return true
	}
	expected, stored = slices.Clone(expected), slices.Clone(stored)
	slices.Sort(expected)
	slices.Sort(stored)
	return slices.Equal(slices.Compact(expected), slices.Compact(stored))
}
