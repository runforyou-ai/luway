//go:build server

package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

const conversationMessagePageSize = 50

// ListConversationMessagesQuery 分页读取成员可见的会话消息。
type ListConversationMessagesQuery struct {
	db *bun.DB
}

type conversationMessageRow struct {
	ReplyUnavailable                     bool                             `bun:"reply_unavailable"`
	ExternalReplyID                      *string                          `bun:"external_reply_id"`
	ExternalReplyBody                    string                           `bun:"external_reply_body"`
	ExternalReplySenderName              string                           `bun:"external_reply_sender_name"`
	ClientMessageID                      *string                          `bun:"client_message_id"`
	ReplyToType                          domain.MessageType               `bun:"reply_to_type"`
	ID                                   string                           `bun:"id"`
	Type                                 string                           `bun:"type"`
	Visibility                           domain.MessageVisibility         `bun:"visibility"`
	ReplyToVisibility                    domain.MessageVisibility         `bun:"reply_to_visibility"`
	Body                                 string                           `bun:"body"`
	Language                             *string                          `bun:"language"`
	SystemEventType                      *string                          `bun:"system_event_type"`
	SystemEventPayload                   json.RawMessage                  `bun:"system_event_payload"`
	OriginatedAt                         time.Time                        `bun:"originated_at"`
	MessageSeq                           int64                            `bun:"message_seq"`
	SourceOrder                          int64                            `bun:"source_order"`
	CreatedAt                            time.Time                        `bun:"created_at"`
	SenderSubjectID                      *string                          `bun:"sender_subject_id"`
	SenderKind                           *string                          `bun:"sender_kind"`
	SenderSourceID                       *string                          `bun:"sender_source_id"`
	SenderDisplayName                    *string                          `bun:"sender_display_name"`
	SenderContactNumber                  *int64                           `bun:"sender_contact_number"`
	SenderAvatarFileID                   *string                          `bun:"sender_avatar_file_id"`
	SenderIdentityType                   *domain.OrganizationIdentityType `bun:"sender_identity_type"`
	SenderPersonalResponsibleName        *string                          `bun:"sender_personal_responsible_name"`
	ReplyToMessageID                     *string                          `bun:"reply_to_message_id"`
	MentionAll                           bool                             `bun:"mention_all"`
	ReplyToDeleted                       bool                             `bun:"reply_to_deleted"`
	ReplyToBody                          *string                          `bun:"reply_to_body"`
	ReplyToSenderSubjectID               *string                          `bun:"reply_to_sender_subject_id"`
	ReplyToSenderKind                    *string                          `bun:"reply_to_sender_kind"`
	ReplyToSenderSourceID                *string                          `bun:"reply_to_sender_source_id"`
	ReplyToSenderDisplayName             *string                          `bun:"reply_to_sender_display_name"`
	ReplyToSenderContactNumber           *int64                           `bun:"reply_to_sender_contact_number"`
	ReplyToSenderAvatarFileID            *string                          `bun:"reply_to_sender_avatar_file_id"`
	ReplyToSenderIdentityType            *domain.OrganizationIdentityType `bun:"reply_to_sender_identity_type"`
	ReplyToSenderPersonalResponsibleName *string                          `bun:"reply_to_sender_personal_responsible_name"`
	ServiceSessionOpeningMessageID       *string                          `bun:"service_session_opening_message_id"`
	ServiceSessionSequence               *int64                           `bun:"service_session_sequence"`
	ServiceSessionStartedAt              *time.Time                       `bun:"service_session_started_at"`
	ServiceSessionStatus                 *string                          `bun:"service_session_status"`
}

// NewListConversationMessagesQuery 创建成员消息历史查询。
func NewListConversationMessagesQuery(db *bun.DB) *ListConversationMessagesQuery {
	return &ListConversationMessagesQuery{db: db}
}

// Execute 在同一只读快照中读取消息、关系和双向边界。
func (q *ListConversationMessagesQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ConversationMessageHistoryInput) (ConversationMessageHistory, error) {
	if fields := validateConversationMessageHistoryInput(input); len(fields) > 0 {
		return ConversationMessageHistory{}, &ValidationError{Fields: fields}
	}
	var history ConversationMessageHistory
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := AuthorizeConversationHistory(ctx, tx, identity, input.ConversationID); err != nil {
			return err
		}

		rows, err := loadConversationWindowRows(ctx, tx, identity, input)
		if err != nil {
			return err
		}
		history, err = buildConversationMessageHistory(rows)
		if err != nil {
			return err
		}
		// 范围内已无可见消息时保留请求的两端，续读位置仍从原边界计算。
		if len(rows) == 0 && input.Start != nil {
			history.Before, history.After = input.Start, input.End
		}
		if history.Before != nil {
			history.HasEarlier, err = conversationMessagesQuery(tx, identity, input.ConversationID).Where("?", messageCursorCondition(*history.Before, "<")).Exists(ctx)
			if err != nil {
				return err
			}
			history.HasLater, err = conversationMessagesQuery(tx, identity, input.ConversationID).Where("?", messageCursorCondition(*history.After, ">")).Exists(ctx)
			if err != nil {
				return err
			}
		}
		if err := loadConversationMessageMentions(ctx, tx, identity.Organization.ID, history.Messages); err != nil {
			return err
		}
		if err := LoadMessageAttachments(ctx, tx, identity.Organization.ID, history.Messages); err != nil {
			return err
		}
		if err := loadMessageTranslations(ctx, tx, identity, history.Messages); err != nil {
			return err
		}
		if err := loadMessageDeliveries(ctx, tx, identity.Organization.ID, input.ConversationID, history.Messages); err != nil {
			return err
		}
		return loadConversationAgentProcesses(ctx, tx, identity.Organization.ID, input.ConversationID, &history)
	})
	if err != nil {
		return ConversationMessageHistory{}, fmt.Errorf("read conversation message window: %w", err)
	}
	return history, nil
}

// MessageReplyUnavailable 按消息历史的同一判定读取成员可见消息当前是否不可被引用。
func MessageReplyUnavailable(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID, messageID string) (bool, error) {
	var row conversationMessageRow
	if err := conversationMessagesQuery(db, identity, conversationID).Where("msg.id = ?", messageID).Scan(ctx, &row); err != nil {
		return false, err
	}
	return row.ReplyUnavailable, nil
}

// conversationMessagesQuery 共用消息正文、发送者、引用和系统事件查询，只返回当前成员在该会话中可见的消息。
func conversationMessagesQuery(db bun.IDB, identity *servermodels.Identity, conversationID string) *bun.SelectQuery {
	return db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id AS id").
		ColumnExpr("cm.reply_provider_message_id AS external_reply_id, cm.reply_body AS external_reply_body, cm.reply_sender_name AS external_reply_sender_name").
		ColumnExpr("COALESCE(msg.visibility = ? AND ch.type = ? AND (cm.message_id IS NULL OR cm.provider_account_id <> tcs.bot_id::text OR tcs.bot_id IS NULL OR cm.channel_id <> ch.id OR cm.provider_conversation_id <> route_cci.external_id OR msg.type NOT IN (?, ?)), FALSE) AS reply_unavailable", domain.MessageVisibilityShared, domain.ChannelTypeTelegram, domain.MessageTypeText, domain.MessageTypeAttachment).
		ColumnExpr("CASE WHEN cs.kind = ? AND cs.source_id = ? THEN msg.client_message_id END AS client_message_id", domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID).
		ColumnExpr("msg.type AS type").
		ColumnExpr("msg.visibility AS visibility").
		ColumnExpr("msg.body AS body").
		ColumnExpr("msg.language AS language").
		ColumnExpr("msg.system_event_type AS system_event_type").
		ColumnExpr("msg.system_event_payload AS system_event_payload").
		ColumnExpr("msg.originated_at AS originated_at").
		ColumnExpr("msg.source_order AS source_order").
		ColumnExpr("msg.message_seq AS message_seq").
		ColumnExpr("msg.created_at AS created_at").
		ColumnExpr("cs.id AS sender_subject_id").
		ColumnExpr("cs.kind AS sender_kind").
		ColumnExpr("cs.source_id AS sender_source_id").
		ColumnExpr("CASE WHEN cs.kind = ? THEN "+contactname.Expr("c", "cci.display_name")+" WHEN cs.kind = ? THEN oi.display_name END AS sender_display_name", domain.ChatSubjectKindContact, domain.ChatSubjectKindOrganizationIdentity).
		ColumnExpr("c.number AS sender_contact_number").
		ColumnExpr("CASE WHEN cs.kind = ? THEN cci.avatar_file_id ELSE oi.avatar_file_id END::text AS sender_avatar_file_id", domain.ChatSubjectKindContact).
		ColumnExpr("oi.type AS sender_identity_type").
		ColumnExpr("? AS sender_personal_responsible_name", PersonalResponsibleName("oi")).
		ColumnExpr("msg.reply_to_message_id AS reply_to_message_id").
		ColumnExpr("msg.mention_all AS mention_all").
		ColumnExpr("? AS reply_to_body", messagequery.Summary("reply_msg")).
		ColumnExpr("reply_msg.deleted_at IS NOT NULL AS reply_to_deleted, reply_msg.type AS reply_to_type, reply_msg.visibility AS reply_to_visibility").
		ColumnExpr("reply_cs.id AS reply_to_sender_subject_id").
		ColumnExpr("reply_cs.kind AS reply_to_sender_kind").
		ColumnExpr("reply_cs.source_id AS reply_to_sender_source_id").
		ColumnExpr("CASE WHEN reply_cs.kind = ? THEN "+contactname.Expr("reply_c", "reply_cci.display_name")+" ELSE reply_oi.display_name END AS reply_to_sender_display_name", domain.ChatSubjectKindContact).
		ColumnExpr("reply_c.number AS reply_to_sender_contact_number").
		ColumnExpr("CASE WHEN reply_cs.kind = ? THEN reply_cci.avatar_file_id ELSE reply_oi.avatar_file_id END::text AS reply_to_sender_avatar_file_id", domain.ChatSubjectKindContact).
		ColumnExpr("reply_oi.type AS reply_to_sender_identity_type").
		ColumnExpr("? AS reply_to_sender_personal_responsible_name", PersonalResponsibleName("reply_oi")).
		ColumnExpr("ss.opening_message_id AS service_session_opening_message_id").
		ColumnExpr("ss.sequence AS service_session_sequence").
		ColumnExpr("ss.created_at AS service_session_started_at").
		ColumnExpr("ss.status AS service_session_status").
		Join("LEFT JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = msg.service_session_id AND ss.organization_id = msg.organization_id AND ss.conversation_id = msg.conversation_id").
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id AND cci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contact_channel_identities AS route_cci ON route_cci.id = cc.contact_channel_identity_id AND route_cci.organization_id = cc.organization_id").
		Join("LEFT JOIN channels AS ch ON ch.id = route_cci.channel_id AND ch.organization_id = route_cci.organization_id").
		Join("LEFT JOIN telegram_channel_settings AS tcs ON tcs.channel_id = ch.id AND tcs.organization_id = ch.organization_id").
		Join("LEFT JOIN channel_messages AS cm ON cm.message_id = msg.id AND cm.organization_id = msg.organization_id AND cm.conversation_id = msg.conversation_id").
		Join("LEFT JOIN contacts AS c ON c.id = cs.source_id AND c.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN organization_identities AS oi ON oi.id = cs.source_id AND oi.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN messages AS reply_msg ON reply_msg.organization_id = msg.organization_id AND reply_msg.conversation_id = msg.conversation_id AND reply_msg.id = msg.reply_to_message_id AND reply_msg.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.organization_id = reply_msg.organization_id AND reply_cp.conversation_id = reply_msg.conversation_id AND reply_cp.id = reply_msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.organization_id = reply_cp.organization_id AND reply_cs.id = reply_cp.subject_id").
		Join("LEFT JOIN organization_identities AS reply_oi ON reply_oi.organization_id = reply_cs.organization_id AND reply_oi.id = reply_cs.source_id AND reply_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN contact_channel_identities AS reply_cci ON reply_cci.id = cc.contact_channel_identity_id AND reply_cci.organization_id = cc.organization_id AND reply_cci.contact_id = reply_cs.source_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS reply_c ON reply_c.id = reply_cs.source_id AND reply_c.organization_id = reply_cs.organization_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Where("msg.organization_id = ?", identity.Organization.ID).
		Where("msg.conversation_id = ?", conversationID).
		Where("msg.type IN (?)", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeSystem, domain.MessageTypeAgentError, domain.MessageTypeAgentCancelled, domain.MessageTypeAttachment})).
		Where("?", messagequery.VisibleTo("msg", identity.OrganizationIdentity.ID)).
		Where("msg.deleted_at IS NULL")
}

// messageCursorCondition 按消息序号构造时间线边界。
func messageCursorCondition(point MessageCursorPoint, operator string) schema.QueryWithArgs {
	return bun.SafeQuery("msg.message_seq "+operator+" ?", point.MessageSeq)
}

// loadConversationWindowRows 按当前方向或目标读取连续消息行。
func loadConversationWindowRows(ctx context.Context, db bun.IDB, identity *servermodels.Identity, input ConversationMessageHistoryInput) ([]conversationMessageRow, error) {
	order := "msg.message_seq"
	var rows []conversationMessageRow
	if input.AroundMessageID != "" {
		var target conversationMessageRow
		err := conversationMessagesQuery(db, identity, input.ConversationID).Where("msg.id = ?", input.AroundMessageID).Scan(ctx, &target)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMessageUnavailable
		}
		if err != nil {
			return nil, err
		}
		point := MessageCursorPoint{ID: target.ID, MessageSeq: target.MessageSeq}
		if err := conversationMessagesQuery(db, identity, input.ConversationID).Where("?", messageCursorCondition(point, "<")).OrderExpr(order+" DESC").Limit(25).Scan(ctx, &rows); err != nil {
			return nil, err
		}
		slices.Reverse(rows)
		rows = append(rows, target)
		var later []conversationMessageRow
		if err := conversationMessagesQuery(db, identity, input.ConversationID).Where("?", messageCursorCondition(point, ">")).OrderExpr(order).Limit(25).Scan(ctx, &later); err != nil {
			return nil, err
		}
		return append(rows, later...), nil
	}
	if input.Start != nil {
		err := conversationMessagesQuery(db, identity, input.ConversationID).
			Where("?", messageCursorCondition(*input.Start, ">=")).
			Where("?", messageCursorCondition(*input.End, "<=")).
			OrderExpr(order).
			Scan(ctx, &rows)
		return rows, err
	}
	query := conversationMessagesQuery(db, identity, input.ConversationID)
	if input.Before != nil {
		query = query.Where("?", messageCursorCondition(*input.Before, "<"))
	}
	if input.After != nil {
		query = query.Where("?", messageCursorCondition(*input.After, ">"))
	} else {
		order = order + " DESC"
	}
	if err := query.OrderExpr(order).Limit(conversationMessagePageSize).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	if input.After == nil {
		slices.Reverse(rows)
	}
	return rows, nil
}

// AuthorizeConversationHistory 对不同会话类型应用各自的成员可见性规则。
func AuthorizeConversationHistory(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) error {
	var conversationType string
	err := db.NewSelect().
		TableExpr("conversations AS cv").
		ColumnExpr("cv.type").
		Where("cv.organization_id = ?", identity.Organization.ID).
		Where("cv.id = ?", conversationID).
		Scan(ctx, &conversationType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("load conversation type for history: %w", err)
	}

	// 承载服务会话的会话对企业成员开放阅读。
	serviceAvailable, err := db.NewSelect().
		TableExpr("service_conversations AS svc").
		Where("svc.organization_id = ?", identity.Organization.ID).
		Where("svc.conversation_id = ?", conversationID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check service conversation access: %w", err)
	}
	if serviceAvailable {
		return nil
	}
	switch domain.ConversationType(conversationType) {
	case domain.ConversationTypeCopilot:
		// Copilot 线程沿用所属服务会话的阅读范围。
		available, err := db.NewSelect().
			TableExpr("service_copilot_threads AS sct").
			Join("JOIN service_conversations AS svc ON svc.organization_id = sct.organization_id AND svc.conversation_id = sct.served_conversation_id").
			Where("sct.organization_id = ?", identity.Organization.ID).
			Where("sct.conversation_id = ?", conversationID).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check copilot thread access: %w", err)
		}
		if !available {
			return ErrConversationNotFound
		}
		return nil
	case domain.ConversationTypeGroup:
		available, err := chatstate.GroupQuery(db, identity, conversationID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("check group conversation access: %w", err)
		}
		if !available {
			return ErrConversationNotFound
		}
		return nil
	case domain.ConversationTypeDirect, domain.ConversationTypeAgent:
		available, err := db.NewSelect().
			TableExpr("conversation_participants AS cp").
			Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
			Where("cp.organization_id = ?", identity.Organization.ID).
			Where("cp.conversation_id = ?", conversationID).
			Where("cp.left_at IS NULL").
			Where("cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
			Where("cs.source_id = ?", identity.OrganizationIdentity.ID).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check internal conversation access: %w", err)
		}
		if !available {
			return ErrConversationNotFound
		}
		return nil
	default:
		return ErrConversationNotFound
	}
}

// validateConversationMessageHistoryInput 校验成员消息历史输入。
func validateConversationMessageHistoryInput(input ConversationMessageHistoryInput) map[string]ValidationCode {
	fields := map[string]ValidationCode{}
	if !common.ValidUUID(input.ConversationID) {
		fields["conversationId"] = ValidationConversationIDInvalid
	}
	if (input.Before != nil && input.After != nil) || (input.AroundMessageID != "" && (input.Before != nil || input.After != nil || !common.ValidUUID(input.AroundMessageID))) {
		fields["cursor"] = ValidationCursorInvalid
	}
	// 范围读取必须同时给出两端且起点不晚于终点，不与其他读取方式组合。
	if (input.Start == nil) != (input.End == nil) ||
		(input.Start != nil && (input.Before != nil || input.After != nil || input.AroundMessageID != "" || input.Start.MessageSeq > input.End.MessageSeq)) {
		fields["cursor"] = ValidationCursorInvalid
	}
	for _, cursor := range []*MessageCursorPoint{input.Before, input.After, input.Start, input.End} {
		if cursor != nil && (!common.ValidUUID(cursor.ID) || cursor.MessageSeq <= 0) {
			fields["cursor"] = ValidationCursorInvalid
		}
	}
	return fields
}

// buildConversationMessageHistory 构造正序成员消息页。
func buildConversationMessageHistory(rows []conversationMessageRow) (ConversationMessageHistory, error) {
	messages := make([]ConversationMessage, 0, len(rows))
	for _, row := range rows {
		message := ConversationMessage{
			ReplyUnavailable: row.ReplyUnavailable, ClientMessageID: row.ClientMessageID, ID: row.ID, Type: domain.MessageType(row.Type), Visibility: row.Visibility, Body: row.Body, Language: row.Language,
			OriginatedAt: row.OriginatedAt, SourceOrder: row.SourceOrder, CreatedAt: row.CreatedAt, MentionAll: row.MentionAll, MessageSeq: row.MessageSeq,
		}
		if message.Type == domain.MessageTypeSystem {
			if row.SystemEventType == nil || len(row.SystemEventPayload) == 0 {
				return ConversationMessageHistory{}, fmt.Errorf("load conversation system event: %w", ErrDataInvariant)
			}
			event := &ConversationSystemEvent{Type: domain.ConversationSystemEventType(*row.SystemEventType)}
			if err := json.Unmarshal(row.SystemEventPayload, event); err != nil {
				return ConversationMessageHistory{}, fmt.Errorf("decode conversation system event: %w", err)
			}
			message.SystemEvent = event
		}
		if row.SenderSubjectID != nil && row.SenderKind != nil && row.SenderSourceID != nil {
			message.Sender = &ConversationMessageSender{
				ChatSubjectID: *row.SenderSubjectID,
				Kind:          domain.ChatSubjectKind(*row.SenderKind),
				SourceID:      *row.SenderSourceID,
				DisplayName:   row.SenderDisplayName, ContactNumber: row.SenderContactNumber, AvatarFileID: row.SenderAvatarFileID, IdentityType: row.SenderIdentityType,
				PersonalResponsibleName: row.SenderPersonalResponsibleName,
			}
		}
		if row.ReplyToMessageID == nil && row.ExternalReplyID != nil {
			message.ReplyTo = &ConversationMessageReference{Type: domain.MessageTypeText, Body: row.ExternalReplyBody, ExternalSenderName: row.ExternalReplySenderName}
		}
		if row.ReplyToMessageID != nil && row.ReplyToDeleted {
			message.ReplyTo = &ConversationMessageReference{ID: *row.ReplyToMessageID, Type: row.ReplyToType, Visibility: row.ReplyToVisibility, Deleted: true}
		} else if row.ReplyToMessageID != nil {
			if row.ReplyToBody == nil || row.ReplyToSenderSubjectID == nil || row.ReplyToSenderKind == nil || row.ReplyToSenderSourceID == nil {
				return ConversationMessageHistory{}, fmt.Errorf("load conversation reply reference: %w", ErrDataInvariant)
			}
			message.ReplyTo = &ConversationMessageReference{
				ID: *row.ReplyToMessageID, Type: row.ReplyToType, Visibility: row.ReplyToVisibility, Body: *row.ReplyToBody,
				Sender: &ConversationMessageSender{
					ChatSubjectID: *row.ReplyToSenderSubjectID,
					Kind:          domain.ChatSubjectKind(*row.ReplyToSenderKind),
					SourceID:      *row.ReplyToSenderSourceID,
					DisplayName:   row.ReplyToSenderDisplayName, ContactNumber: row.ReplyToSenderContactNumber, AvatarFileID: row.ReplyToSenderAvatarFileID, IdentityType: row.ReplyToSenderIdentityType,
					PersonalResponsibleName: row.ReplyToSenderPersonalResponsibleName,
				},
			}
		}
		if row.ServiceSessionOpeningMessageID != nil && *row.ServiceSessionOpeningMessageID == row.ID {
			message.SessionStart = &ConversationMessageSessionStart{
				Sequence:  *row.ServiceSessionSequence,
				StartedAt: *row.ServiceSessionStartedAt,
				Status:    domain.ServiceSessionStatus(*row.ServiceSessionStatus),
			}
		}
		messages = append(messages, message)
	}

	result := ConversationMessageHistory{Messages: messages}
	if len(rows) == 0 {
		return result, nil
	}
	first := rows[0]
	last := rows[len(rows)-1]
	result.Before = &MessageCursorPoint{ID: first.ID, MessageSeq: first.MessageSeq}
	result.After = &MessageCursorPoint{ID: last.ID, MessageSeq: last.MessageSeq}
	return result, nil
}
