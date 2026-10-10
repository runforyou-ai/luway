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

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// conversationMessagePageSize 是按方向读取成员消息历史时每页的消息数。
const conversationMessagePageSize = 50

// ListConversationMessagesQuery 分页读取成员可见的会话消息。
type ListConversationMessagesQuery struct {
	db *bun.DB
}

// conversationMessageRow 是成员消息历史查询的一行：消息正文、发送者、引用、外部平台引用与所属服务周期。
type conversationMessageRow struct {
	ReplyUnavailable                     bool                          `bun:"reply_unavailable"`
	ExternalReplyID                      *string                       `bun:"external_reply_id"`
	ExternalReplyBody                    string                        `bun:"external_reply_body"`
	ExternalReplySenderName              string                        `bun:"external_reply_sender_name"`
	ClientMessageID                      *string                       `bun:"client_message_id"`
	LocalAgentToolCallID                 *string                       `bun:"local_agent_tool_call_id"`
	LocalAgent                           *string                       `bun:"local_agent"`
	ReplyToType                          domain.MessageType            `bun:"reply_to_type"`
	ID                                   string                        `bun:"id"`
	Type                                 string                        `bun:"type"`
	Visibility                           domain.MessageVisibility      `bun:"visibility"`
	ReplyToVisibility                    domain.MessageVisibility      `bun:"reply_to_visibility"`
	Body                                 string                        `bun:"body"`
	Language                             *string                       `bun:"language"`
	SystemEventType                      *string                       `bun:"system_event_type"`
	SystemEventPayload                   json.RawMessage               `bun:"system_event_payload"`
	OriginatedAt                         time.Time                     `bun:"originated_at"`
	MessageSeq                           int64                         `bun:"message_seq"`
	CreatedAt                            time.Time                     `bun:"created_at"`
	SenderSubjectID                      *string                       `bun:"sender_subject_id"`
	SenderKind                           *string                       `bun:"sender_kind"`
	SenderSourceID                       *string                       `bun:"sender_source_id"`
	SenderDisplayName                    *string                       `bun:"sender_display_name"`
	SenderContactNumber                  *int64                        `bun:"sender_contact_number"`
	SenderAvatarFileID                   *string                       `bun:"sender_avatar_file_id"`
	SenderIdentityType                   *domain.WorkspaceIdentityType `bun:"sender_identity_type"`
	SenderPersonalResponsibleName        *string                       `bun:"sender_personal_responsible_name"`
	ReplyToMessageID                     *string                       `bun:"reply_to_message_id"`
	MentionAll                           bool                          `bun:"mention_all"`
	ReplyToDeleted                       bool                          `bun:"reply_to_deleted"`
	ReplyToBody                          *string                       `bun:"reply_to_body"`
	ReplyToSenderSubjectID               *string                       `bun:"reply_to_sender_subject_id"`
	ReplyToSenderKind                    *string                       `bun:"reply_to_sender_kind"`
	ReplyToSenderSourceID                *string                       `bun:"reply_to_sender_source_id"`
	ReplyToSenderDisplayName             *string                       `bun:"reply_to_sender_display_name"`
	ReplyToSenderContactNumber           *int64                        `bun:"reply_to_sender_contact_number"`
	ReplyToSenderAvatarFileID            *string                       `bun:"reply_to_sender_avatar_file_id"`
	ReplyToSenderIdentityType            *domain.WorkspaceIdentityType `bun:"reply_to_sender_identity_type"`
	ReplyToSenderPersonalResponsibleName *string                       `bun:"reply_to_sender_personal_responsible_name"`
	ServiceSessionOpeningMessageID       *string                       `bun:"service_session_opening_message_id"`
	ServiceSessionSequence               *int64                        `bun:"service_session_sequence"`
	ServiceSessionStartedAt              *time.Time                    `bun:"service_session_started_at"`
	ServiceSessionStatus                 *string                       `bun:"service_session_status"`
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
		if err := conversationaccess.RequireReadable(ctx, tx, identity, input.ConversationID); err != nil {
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
		if err := loadConversationMessageMentions(ctx, tx, identity.Workspace.ID, history.Messages); err != nil {
			return err
		}
		if err := LoadMessageAttachments(ctx, tx, identity.Workspace.ID, history.Messages); err != nil {
			return err
		}
		if err := loadMessageTranslations(ctx, tx, identity, history.Messages); err != nil {
			return err
		}
		if err := loadMessageDeliveries(ctx, tx, identity.Workspace.ID, input.ConversationID, history.Messages); err != nil {
			return err
		}
		if err := loadMessageToolCalls(ctx, tx, identity, history.Messages); err != nil {
			return err
		}
		messageIDs := arr.Map(history.Messages, func(message ConversationMessage) string { return message.ID })
		processes, err := processquery.LoadConversation(ctx, tx, identity.Workspace.ID, input.ConversationID, messageIDs)
		if err != nil {
			return err
		}
		history.AgentRuns, history.PendingAgents = processes.Runs, processes.PendingAgents
		// 为 AI 回复补充运行过程引用、交给本机 Agent 的轮次与失败原因。
		for i := range history.Messages {
			if process := processes.Messages[history.Messages[i].ID]; process != nil {
				history.Messages[i].AgentProcess, history.Messages[i].LocalAgentTurns, history.Messages[i].AgentErrorCode = process.Process, process.LocalAgentTurns, process.ErrorCode
			}
		}
		return nil
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
		// 渠道不支持引用时共享消息都不可引用；经外部平台投递的渠道只能引用当前平台账号与对方编号下有平台消息身份的文本与附件。
		ColumnExpr("COALESCE(msg.visibility = ? AND (ch.type IN (?) OR ch.type IN (?) AND (cm.message_id IS NULL OR ch.provider_account_id IS NULL OR cm.provider_account_id <> ch.provider_account_id OR cm.channel_id <> ch.id OR cm.provider_conversation_id <> route_cci.external_id OR msg.type NOT IN (?, ?))), FALSE) AS reply_unavailable",
			domain.MessageVisibilityShared, bun.List(domain.ChannelTypesWith(func(c domain.ChannelCapabilities) bool { return !c.Quote })),
			bun.List(domain.ChannelTypesWith(domain.ChannelCapabilities.ViaPlatform)), domain.MessageTypeText, domain.MessageTypeAttachment).
		ColumnExpr("CASE WHEN cs.kind = ? AND cs.source_id = ? THEN msg.client_message_id END AS client_message_id", domain.ChatSubjectKindWorkspaceIdentity, identity.WorkspaceIdentity.ID).
		ColumnExpr("msg.type AS type").
		ColumnExpr("msg.agent_tool_call_id AS local_agent_tool_call_id, las.local_agent AS local_agent").
		ColumnExpr("msg.visibility AS visibility").
		ColumnExpr("msg.body AS body").
		ColumnExpr("msg.language AS language").
		ColumnExpr("msg.system_event_type AS system_event_type").
		ColumnExpr("msg.system_event_payload AS system_event_payload").
		ColumnExpr("msg.originated_at AS originated_at").
		ColumnExpr("msg.message_seq AS message_seq").
		ColumnExpr("msg.created_at AS created_at").
		ColumnExpr("cs.id AS sender_subject_id").
		ColumnExpr("cs.kind AS sender_kind").
		ColumnExpr("cs.source_id AS sender_source_id").
		ColumnExpr("CASE WHEN cs.kind = ? THEN "+contactname.Expr("c", "ci.display_name")+" WHEN cs.kind = ? THEN oi.display_name END AS sender_display_name", domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity).
		ColumnExpr("c.number AS sender_contact_number").
		ColumnExpr("CASE WHEN cs.kind = ? THEN ci.avatar_file_id ELSE oi.avatar_file_id END::text AS sender_avatar_file_id", domain.ChatSubjectKindContact).
		ColumnExpr("oi.type AS sender_identity_type").
		ColumnExpr("? AS sender_personal_responsible_name", chatstate.PersonalResponsibleName("oi")).
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
		ColumnExpr("? AS reply_to_sender_personal_responsible_name", chatstate.PersonalResponsibleName("reply_oi")).
		ColumnExpr("ss.opening_message_id AS service_session_opening_message_id").
		ColumnExpr("ss.sequence AS service_session_sequence").
		ColumnExpr("ss.created_at AS service_session_started_at").
		ColumnExpr("ss.status AS service_session_status").
		Join("LEFT JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
		Join("LEFT JOIN agent_tool_calls AS las_atc ON las_atc.id = msg.agent_tool_call_id AND las_atc.workspace_id = msg.workspace_id").
		Join("LEFT JOIN local_agent_sessions AS las ON las.id = las_atc.local_agent_session_id AND las.workspace_id = las_atc.workspace_id").
		Join("LEFT JOIN service_sessions AS ss ON ss.id = msg.service_session_id AND ss.workspace_id = msg.workspace_id AND ss.conversation_id = msg.conversation_id").
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.workspace_id = msg.workspace_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id AND ci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN channel_identities AS route_cci ON route_cci.id = cc.channel_identity_id AND route_cci.workspace_id = cc.workspace_id").
		Join("LEFT JOIN channels AS ch ON ch.id = route_cci.channel_id AND ch.workspace_id = route_cci.workspace_id").
		Join("LEFT JOIN channel_messages AS cm ON cm.message_id = msg.id AND cm.part = 1 AND cm.workspace_id = msg.workspace_id AND cm.conversation_id = msg.conversation_id").
		Join("LEFT JOIN contacts AS c ON c.id = cs.source_id AND c.workspace_id = cs.workspace_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN messages AS reply_msg ON reply_msg.workspace_id = msg.workspace_id AND reply_msg.conversation_id = msg.conversation_id AND reply_msg.id = msg.reply_to_message_id AND reply_msg.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.workspace_id = reply_msg.workspace_id AND reply_cp.conversation_id = reply_msg.conversation_id AND reply_cp.id = reply_msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.workspace_id = reply_cp.workspace_id AND reply_cs.id = reply_cp.subject_id").
		Join("LEFT JOIN workspace_identities AS reply_oi ON reply_oi.workspace_id = reply_cs.workspace_id AND reply_oi.id = reply_cs.source_id AND reply_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_identities AS reply_cci ON reply_cci.id = cc.channel_identity_id AND reply_cci.workspace_id = cc.workspace_id AND reply_cci.contact_id = reply_cs.source_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS reply_c ON reply_c.id = reply_cs.source_id AND reply_c.workspace_id = reply_cs.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Where("msg.workspace_id = ?", identity.Workspace.ID).
		Where("msg.conversation_id = ?", conversationID).
		Where("msg.type IN (?)", bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeSystem, domain.MessageTypeAgentError, domain.MessageTypeAgentCancelled, domain.MessageTypeAttachment, domain.MessageTypeUnsupported})).
		Where("?", messagequery.VisibleTo("msg", identity.WorkspaceIdentity.ID)).
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

// validateConversationMessageHistoryInput 校验成员消息历史输入。
func validateConversationMessageHistoryInput(input ConversationMessageHistoryInput) map[string]ValidationCode {
	fields := map[string]ValidationCode{}
	if (input.Before != nil && input.After != nil) || (input.AroundMessageID != "" && (input.Before != nil || input.After != nil)) {
		fields["cursor"] = ValidationCursorInvalid
	}
	// 范围读取必须同时给出两端且起点不晚于终点，不与其他读取方式组合。
	if (input.Start == nil) != (input.End == nil) ||
		(input.Start != nil && (input.Before != nil || input.After != nil || input.AroundMessageID != "" || input.Start.MessageSeq > input.End.MessageSeq)) {
		fields["cursor"] = ValidationCursorInvalid
	}
	for _, cursor := range []*MessageCursorPoint{input.Before, input.After, input.Start, input.End} {
		if cursor != nil && (!str.IsUUID(cursor.ID) || cursor.MessageSeq <= 0) {
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
			OriginatedAt: row.OriginatedAt, CreatedAt: row.CreatedAt, MentionAll: row.MentionAll, MessageSeq: row.MessageSeq,
		}
		if row.LocalAgentToolCallID != nil && row.LocalAgent != nil {
			message.LocalAgentReply = &processquery.LocalAgentTurn{ToolCallID: *row.LocalAgentToolCallID, LocalAgent: *row.LocalAgent}
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

// loadMessageToolCalls 为工具调用事件补充所指工具调用的当前内容与当前成员可执行的处理。
func loadMessageToolCalls(ctx context.Context, db bun.IDB, identity *servermodels.Identity, messages []ConversationMessage) error {
	ids := make([]string, 0)
	for _, message := range messages {
		if message.SystemEvent != nil && message.SystemEvent.ToolCallID != nil {
			ids = append(ids, *message.SystemEvent.ToolCallID)
		}
	}
	decisions, err := agentprocess.LoadToolDecisions(ctx, db, identity, ids)
	if err != nil {
		return err
	}
	for index := range messages {
		event := messages[index].SystemEvent
		if event == nil || event.ToolCallID == nil {
			continue
		}
		if decision, ok := decisions[*event.ToolCallID]; ok {
			event.ToolCall = &decision
		}
	}
	return nil
}
