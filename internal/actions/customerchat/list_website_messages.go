//go:build server

package customerchat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	"github.com/uptrace/bun"
)

const websiteMessagePageSize = 50

// ListWebsiteMessagesQuery 分页读取网站访客客户线程消息。
type ListWebsiteMessagesQuery struct {
	db *bun.DB
}

type websiteMessageRow struct {
	ClientMessageID         *string                          `bun:"client_message_id"`
	MessageSeq              int64                            `bun:"message_seq"`
	ID                      string                           `bun:"id"`
	Type                    domain.MessageType               `bun:"type"`
	Body                    string                           `bun:"body"`
	SystemEventType         *string                          `bun:"system_event_type"`
	SystemEventPayload      json.RawMessage                  `bun:"system_event_payload"`
	ServiceSessionID        string                           `bun:"service_session_id"`
	SenderIdentityType      *domain.OrganizationIdentityType `bun:"sender_identity_type"`
	SenderIdentityID        *string                          `bun:"sender_identity_id"`
	SenderDisplayName       *string                          `bun:"sender_display_name"`
	SenderAvatarBackend     *string                          `bun:"sender_avatar_storage_backend"`
	SenderAvatarStorageKey  *string                          `bun:"sender_avatar_storage_key"`
	OriginatedAt            time.Time                        `bun:"originated_at"`
	SourceOrder             int64                            `bun:"source_order"`
	CreatedAt               time.Time                        `bun:"created_at"`
	SubjectKind             string                           `bun:"subject_kind"`
	ReplyToMessageID        *string                          `bun:"reply_to_message_id"`
	ReplyDeleted            bool                             `bun:"reply_deleted"`
	ReplyBody               string                           `bun:"reply_body"`
	ReplySubjectKind        string                           `bun:"reply_subject_kind"`
	ReplySenderIdentityType *domain.OrganizationIdentityType `bun:"reply_sender_identity_type"`
	AttachmentFileID        *string                          `bun:"attachment_file_id"`
	AttachmentName          *string                          `bun:"attachment_name"`
	AttachmentContentType   *string                          `bun:"attachment_content_type"`
	AttachmentByteSize      *int64                           `bun:"attachment_byte_size"`
	AttachmentImageWidth    *int                             `bun:"attachment_image_width"`
	AttachmentImageHeight   *int                             `bun:"attachment_image_height"`
	AttachmentTransfer      *string                          `bun:"attachment_transfer_status"`
	AttachmentBackend       *string                          `bun:"attachment_storage_backend"`
	AttachmentStorageKey    *string                          `bun:"attachment_storage_key"`
}

// NewListWebsiteMessagesQuery 创建网站访客消息历史查询。
func NewListWebsiteMessagesQuery(db *bun.DB) *ListWebsiteMessagesQuery {
	return &ListWebsiteMessagesQuery{db: db}
}

// Execute 返回指定客户线程的消息页。
func (q *ListWebsiteMessagesQuery) Execute(ctx context.Context, input MessageHistoryInput) (MessageHistory, error) {
	fields := validateMessageHistoryInput(input)
	if len(fields) > 0 {
		return MessageHistory{}, &conversationaction.ValidationError{Fields: fields}
	}
	channel, err := loadWebsiteChannel(ctx, q.db, input.ChannelID)
	if err != nil {
		return MessageHistory{}, err
	}
	identity, found, err := loadWebsiteVisitorIdentity(ctx, q.db, channel, input.ExternalID)
	if err != nil {
		return MessageHistory{}, err
	}
	if !found {
		return MessageHistory{}, conversationaction.ErrConversationNotFound
	}
	owned, err := q.db.NewSelect().
		TableExpr("channel_conversations AS cc").
		Join("JOIN conversations AS cv ON cv.id = cc.conversation_id AND cv.organization_id = cc.organization_id AND cv.type = ?", domain.ConversationTypeChannel).
		Where("cc.organization_id = ?", channel.OrganizationID).
		Where("cc.conversation_id = ?", input.ConversationID).
		Where("cc.contact_channel_identity_id = ?", identity.ID).
		Exists(ctx)
	if err != nil {
		return MessageHistory{}, fmt.Errorf("check website conversation ownership: %w", err)
	}
	if !owned {
		return MessageHistory{}, conversationaction.ErrConversationNotFound
	}

	query := q.db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id AS id").
		ColumnExpr("CASE WHEN cs.kind = ? AND cs.source_id = ? THEN msg.client_message_id END AS client_message_id", domain.ChatSubjectKindContact, identity.ContactID).
		ColumnExpr("msg.message_seq").
		ColumnExpr("msg.type AS type").
		ColumnExpr("msg.body AS body").
		ColumnExpr("msg.system_event_type AS system_event_type").
		ColumnExpr("msg.system_event_payload AS system_event_payload").
		ColumnExpr("ss.id AS service_session_id").
		ColumnExpr("oi.type AS sender_identity_type").
		ColumnExpr("oi.id::text AS sender_identity_id").
		ColumnExpr("oi.display_name AS sender_display_name").
		ColumnExpr("sav.storage_backend AS sender_avatar_storage_backend").
		ColumnExpr("sav.storage_key AS sender_avatar_storage_key").
		ColumnExpr("msg.originated_at AS originated_at").
		ColumnExpr("msg.source_order AS source_order").
		ColumnExpr("msg.created_at AS created_at").
		ColumnExpr("cs.kind AS subject_kind").
		ColumnExpr("msg.reply_to_message_id").
		ColumnExpr("reply.deleted_at IS NOT NULL AS reply_deleted").
		ColumnExpr("? AS reply_body", messagequery.Summary("reply")).
		ColumnExpr("reply_cs.kind AS reply_subject_kind").
		ColumnExpr("reply_oi.type AS reply_sender_identity_type").
		ColumnExpr("ma.file_id::text AS attachment_file_id").
		ColumnExpr("ma.name AS attachment_name").
		ColumnExpr("ma.content_type AS attachment_content_type").
		ColumnExpr("ma.byte_size AS attachment_byte_size").
		ColumnExpr("ma.image_width AS attachment_image_width").
		ColumnExpr("ma.image_height AS attachment_image_height").
		ColumnExpr("ma.transfer_status AS attachment_transfer_status").
		ColumnExpr("af.storage_backend AS attachment_storage_backend").
		ColumnExpr("af.storage_key AS attachment_storage_key").
		Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.organization_id = msg.organization_id").
		Join("LEFT JOIN files AS af ON af.id = ma.file_id AND af.organization_id = ma.organization_id").
		Join("JOIN service_sessions AS ss ON ss.id = msg.service_session_id AND ss.organization_id = msg.organization_id AND ss.conversation_id = msg.conversation_id").
		Join("LEFT JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.id = cs.source_id AND oi.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN files AS sav ON sav.id = oi.avatar_file_id AND sav.organization_id = oi.organization_id AND sav.status = ?", domain.FileStatusActive).
		Join("LEFT JOIN messages AS reply ON reply.id = msg.reply_to_message_id AND reply.organization_id = msg.organization_id AND reply.conversation_id = msg.conversation_id AND reply.type IN (?, ?) AND reply.visibility = ?", domain.MessageTypeText, domain.MessageTypeAttachment, domain.MessageVisibilityShared).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.id = reply.sender_participant_id AND reply_cp.organization_id = reply.organization_id AND reply_cp.conversation_id = reply.conversation_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.id = reply_cp.subject_id AND reply_cs.organization_id = reply_cp.organization_id").
		Join("LEFT JOIN organization_identities AS reply_oi ON reply_oi.id = reply_cs.source_id AND reply_oi.organization_id = reply_cs.organization_id AND reply_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Where("msg.organization_id = ?", channel.OrganizationID).
		Where("msg.conversation_id = ?", input.ConversationID).
		// 访客只读对客消息，以及成员加入、周期结束与留下邮箱三类客服处理周期事件；转交和自动分配只在去向为成员时计为成员加入；AI 转人工由对客话术告知访客，不投影事件。
		WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.
				WhereGroup(" OR ", func(query *bun.SelectQuery) *bun.SelectQuery {
					return query.Where("msg.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
						Where("msg.visibility = ?", domain.MessageVisibilityShared)
				}).
				WhereGroup(" OR ", func(query *bun.SelectQuery) *bun.SelectQuery {
					return query.Where("msg.type = ?", domain.MessageTypeSystem).
						WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
							return query.Where("msg.system_event_type IN (?, ?, ?, ?)", domain.ConversationSystemEventServiceSessionClaimed,
								domain.ConversationSystemEventServiceSessionTakenOver, domain.ConversationSystemEventServiceSessionClosed, domain.ConversationSystemEventServiceSessionEmailCollected).
								WhereOr("msg.system_event_type IN (?, ?) AND msg.system_event_payload->'target'->>'kind' = ?", domain.ConversationSystemEventServiceSessionTransferred,
									domain.ConversationSystemEventServiceSessionAssigned, domain.ServiceSessionTargetMember)
						})
				})
		}).
		Where("msg.deleted_at IS NULL")
	if input.Before != nil {
		query = query.Where("msg.message_seq < ?", input.Before.MessageSeq).
			OrderExpr("msg.message_seq DESC")
	} else if input.After != nil {
		query = query.Where("msg.message_seq > ?", input.After.MessageSeq).
			OrderExpr("msg.message_seq ASC")
	} else {
		query = query.OrderExpr("msg.message_seq DESC")
	}
	var rows []websiteMessageRow
	if err := query.Limit(websiteMessagePageSize+1).Scan(ctx, &rows); err != nil {
		return MessageHistory{}, fmt.Errorf("list website conversation messages: %w", err)
	}
	history, err := buildMessageHistory(rows, input)
	if err != nil {
		return MessageHistory{}, err
	}
	history.OrganizationID = channel.OrganizationID
	history.SessionRatings, err = listWebsiteSessionRatings(ctx, q.db, channel.OrganizationID, input.ConversationID)
	if err != nil {
		return MessageHistory{}, err
	}
	return history, nil
}

// listWebsiteSessionRatings 读取线程内已关闭或已评价周期的评价状态，评价挂在周期最近一次结束事件上；重新打开且未评价的周期不返回。
func listWebsiteSessionRatings(ctx context.Context, db bun.IDB, organizationID, conversationID string) ([]VisitorSessionRating, error) {
	var rows []struct {
		ServiceSessionID string                      `bun:"service_session_id"`
		EndMessageID     string                      `bun:"end_message_id"`
		Status           domain.ServiceSessionStatus `bun:"status"`
		RatingResolved   *bool                       `bun:"rating_resolved"`
		RatingComment    *string                     `bun:"rating_comment"`
	}
	if err := db.NewRaw(`SELECT ss.id AS service_session_id, ended.id AS end_message_id, ss.status, ss.rating_resolved, ss.rating_comment
		FROM service_sessions AS ss
		JOIN LATERAL (
			SELECT msg.id FROM messages AS msg
			WHERE msg.organization_id = ss.organization_id AND msg.conversation_id = ss.conversation_id AND msg.service_session_id = ss.id AND msg.system_event_type = ?
			ORDER BY msg.message_seq DESC LIMIT 1
		) AS ended ON TRUE
		WHERE ss.organization_id = ? AND ss.conversation_id = ? AND (ss.status = ? OR ss.rated_at IS NOT NULL)
		ORDER BY ss.sequence`,
		domain.ConversationSystemEventServiceSessionClosed, organizationID, conversationID, domain.ServiceSessionStatusClosed).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list website service session ratings: %w", err)
	}
	ratings := make([]VisitorSessionRating, 0, len(rows))
	for _, row := range rows {
		rating := VisitorSessionRating{ServiceSessionID: row.ServiceSessionID, EndMessageID: row.EndMessageID,
			VisitorRating: VisitorRating{Rateable: row.RatingResolved == nil, Resolved: row.RatingResolved}}
		if row.RatingComment != nil {
			rating.Comment = *row.RatingComment
		}
		ratings = append(ratings, rating)
	}
	return ratings, nil
}

// validateMessageHistoryInput 校验消息分页输入。
func validateMessageHistoryInput(input MessageHistoryInput) map[string]conversationaction.ValidationCode {
	fields := map[string]conversationaction.ValidationCode{}
	if !common.ValidUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if !common.ValidUUID(input.ConversationID) {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	if input.Before != nil && input.After != nil {
		fields["cursor"] = conversationaction.ValidationCursorInvalid
	}
	for _, cursor := range []*conversationaction.MessageCursorPoint{input.Before, input.After} {
		if cursor != nil && (cursor.MessageSeq <= 0 || !common.ValidUUID(cursor.ID)) {
			fields["cursor"] = conversationaction.ValidationCursorInvalid
		}
	}
	return fields
}

// buildMessageHistory 构造正序消息页。
func buildMessageHistory(rows []websiteMessageRow, input MessageHistoryInput) (MessageHistory, error) {
	hasMore := len(rows) > websiteMessagePageSize
	if hasMore {
		rows = rows[:websiteMessagePageSize]
	}
	if input.After == nil {
		slices.Reverse(rows)
	}
	messages := make([]Message, 0, len(rows))
	for _, row := range rows {
		if row.Type == domain.MessageTypeSystem {
			event, err := websiteVisitorEvent(row)
			if err != nil {
				return MessageHistory{}, err
			}
			messages = append(messages, Message{
				MessageSeq: row.MessageSeq, ID: row.ID, Author: domain.MessageAuthorSystem, Event: event,
				OriginatedAt: row.OriginatedAt, SourceOrder: row.SourceOrder, CreatedAt: row.CreatedAt,
			})
			continue
		}
		author := domain.MessageAuthorAgent
		if row.SubjectKind == string(domain.ChatSubjectKindContact) {
			author = domain.MessageAuthorVisitor
		}
		message := Message{
			ClientMessageID: row.ClientMessageID, MessageSeq: row.MessageSeq, ID: row.ID, Author: author, Body: row.Body, SenderIdentityType: row.SenderIdentityType,
			OriginatedAt: row.OriginatedAt, SourceOrder: row.SourceOrder, CreatedAt: row.CreatedAt,
		}
		// 组织身份发送的消息附带发送身份、名称和可用头像位置。
		if row.SenderIdentityID != nil && row.SenderDisplayName != nil {
			message.SenderIdentityID = *row.SenderIdentityID
			message.SenderDisplayName = *row.SenderDisplayName
		}
		if row.SenderAvatarBackend != nil && row.SenderAvatarStorageKey != nil {
			message.SenderAvatar = &FileLocation{StorageBackend: domain.FileStorageBackend(*row.SenderAvatarBackend), StorageKey: *row.SenderAvatarStorageKey}
		}
		// 附件行存在时其余列均非空；取回失败的附件没有文件编号和存储位置。
		if row.AttachmentName != nil {
			message.Attachment = &VisitorAttachment{MessageAttachment: conversationaction.MessageAttachment{
				Name: *row.AttachmentName, ContentType: *row.AttachmentContentType, ByteSize: *row.AttachmentByteSize,
				ImageWidth: *row.AttachmentImageWidth, ImageHeight: *row.AttachmentImageHeight,
				TransferStatus: domain.MessageAttachmentTransferStatus(*row.AttachmentTransfer),
			}}
			if row.AttachmentFileID != nil && row.AttachmentBackend != nil && row.AttachmentStorageKey != nil {
				message.Attachment.ID = *row.AttachmentFileID
				message.Attachment.StorageBackend = domain.FileStorageBackend(*row.AttachmentBackend)
				message.Attachment.StorageKey = *row.AttachmentStorageKey
			}
		}
		if row.ReplyToMessageID != nil {
			message.ReplyTo = &MessageReference{ID: *row.ReplyToMessageID, Deleted: row.ReplyDeleted}
			if !row.ReplyDeleted {
				message.ReplyTo.Author = domain.MessageAuthorAgent
				if row.ReplySubjectKind == string(domain.ChatSubjectKindContact) {
					message.ReplyTo.Author = domain.MessageAuthorVisitor
				}
				message.ReplyTo.Body = row.ReplyBody
				message.ReplyTo.SenderIdentityType = row.ReplySenderIdentityType
			}
		}
		messages = append(messages, message)
	}
	result := MessageHistory{Messages: messages}
	if len(rows) == 0 {
		return result, nil
	}
	first := conversationaction.MessageCursorPoint{MessageSeq: rows[0].MessageSeq, ID: rows[0].ID}
	last := conversationaction.MessageCursorPoint{MessageSeq: rows[len(rows)-1].MessageSeq, ID: rows[len(rows)-1].ID}
	switch {
	case input.Before != nil:
		if hasMore {
			result.Before = &first
		}
	case input.After != nil:
		result.After = &last
	default:
		if hasMore {
			result.Before = &first
		}
		result.After = &last
	}
	return result, nil
}

// websiteVisitorEvent 把客服处理周期系统事件投影为访客可见事件，只保留成员名称与评价状态。
func websiteVisitorEvent(row websiteMessageRow) (*VisitorEvent, error) {
	if row.SystemEventType == nil {
		return nil, fmt.Errorf("load website visitor event: %w", conversationaction.ErrDataInvariant)
	}
	var payload struct {
		ActorDisplayName string                       `json:"actorDisplayName"`
		Target           *domain.ServiceSessionTarget `json:"target"`
		Email            string                       `json:"email"`
	}
	if err := json.Unmarshal(row.SystemEventPayload, &payload); err != nil {
		return nil, fmt.Errorf("decode website visitor event: %w", err)
	}
	event := &VisitorEvent{Type: VisitorEventMemberJoined, ServiceSessionID: row.ServiceSessionID, MemberName: payload.ActorDisplayName}
	switch domain.ConversationSystemEventType(*row.SystemEventType) {
	case domain.ConversationSystemEventServiceSessionTransferred, domain.ConversationSystemEventServiceSessionAssigned:
		if payload.Target == nil || payload.Target.DisplayName == nil {
			return nil, fmt.Errorf("load website visitor event target: %w", conversationaction.ErrDataInvariant)
		}
		event.MemberName = *payload.Target.DisplayName
	case domain.ConversationSystemEventServiceSessionClosed:
		event = &VisitorEvent{Type: VisitorEventSessionEnded, ServiceSessionID: row.ServiceSessionID}
	case domain.ConversationSystemEventServiceSessionEmailCollected:
		event = &VisitorEvent{Type: VisitorEventEmailCollected, ServiceSessionID: row.ServiceSessionID, Email: payload.Email}
	}
	return event, nil
}
