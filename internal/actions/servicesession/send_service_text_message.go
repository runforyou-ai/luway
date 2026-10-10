//go:build server

package servicesession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/membersend"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/languagetag"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// SendServiceTextMessageAction 持久化企业成员的服务会话文本回复。
type SendServiceTextMessageAction struct {
	enqueuer servertask.TxEnqueuer
	db       *bun.DB
}

// memberMessageIDs 是一次发送新建记录使用的聊天主体、参与者与消息编号。
type memberMessageIDs struct {
	subject     string
	participant string
	message     string
}

// customerMessagePayload 定义一次成员客户会话发送的消息内容。
type customerMessagePayload struct {
	Attachment       *membersend.AttachmentIntent
	ConversationID   string
	ClientMessageID  string
	Body             string
	ReplyToMessageID string
	Type             domain.MessageType
	Visibility       domain.MessageVisibility
	// MentionIdentityIDs 是内部备注提醒的企业成员身份。
	MentionIdentityIDs []string
	// Translation 是翻译发送时发给客户的译文，Body 为客服书写的原文。
	Translation *OutgoingTranslation
}

// intent 返回该次发送对应的幂等核对意图。
func (p customerMessagePayload) intent() membersend.Intent {
	return membersend.Intent{
		ConversationID: p.ConversationID, Body: p.Body, ReplyToMessageID: p.ReplyToMessageID, Attachment: p.Attachment,
		Type: p.Type, Visibility: p.Visibility, ServiceSession: membersend.ServiceSessionPresent,
		Translated: p.Translation != nil, MentionIdentityIDs: append([]string{}, p.MentionIdentityIDs...),
	}
}

// NewSendServiceTextMessageAction 创建成员服务会话回复操作。
func NewSendServiceTextMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *SendServiceTextMessageAction {
	return &SendServiceTextMessageAction{db: db, enqueuer: enqueuer}
}

// Execute 在一个事务中写入成员客户会话回复。
func (a *SendServiceTextMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input ServiceTextMessageInput) (conversationaction.ConversationMessage, error) {
	normalized, fields := normalizeServiceTextMessageInput(input)
	if len(fields) > 0 {
		return conversationaction.ConversationMessage{}, &conversationaction.ValidationError{Fields: fields}
	}
	return runCustomerMessage(ctx, a.db, a.enqueuer, identity, customerMessagePayload{
		ConversationID: normalized.ConversationID, ClientMessageID: normalized.ClientMessageID,
		Body: normalized.Body, ReplyToMessageID: normalized.ReplyToMessageID, Type: domain.MessageTypeText,
		Visibility: normalized.Visibility, MentionIdentityIDs: normalized.MentionIdentityIDs, Translation: normalized.Translation,
	}, "message")
}

// SavedTranslation 返回本人以该发送编号已保存的翻译发送的译文与原话语言，未保存或未翻译时返回 nil；重试发送据此沿用首次发出的译文，与当前语言设置无关。
func (a *SendServiceTextMessageAction) SavedTranslation(ctx context.Context, identity *servermodels.Identity, clientMessageID string) (*OutgoingTranslation, error) {
	clientMessageID, valid := str.NormalizeUUID(clientMessageID)
	if !valid {
		return nil, nil
	}
	var rows []struct {
		Language       string `bun:"language"`
		Body           string `bun:"body"`
		SourceLanguage string `bun:"source_language"`
	}
	if err := a.db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.language, msg.body, mt.language AS source_language").
		Join("JOIN message_translations AS mt ON mt.message_id = msg.id AND mt.workspace_id = msg.workspace_id AND mt.authored").
		Where("msg.workspace_id = ? AND msg.idempotency_key = ? AND msg.language IS NOT NULL", identity.Workspace.ID, membersend.Key(identity, clientMessageID)).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load saved outgoing translation: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &OutgoingTranslation{Language: rows[0].Language, SourceLanguage: rows[0].SourceLanguage, Body: rows[0].Body}, nil
}

// runCustomerMessage 在一个事务中发送成员客户会话消息并读取其引用能力，kind 写入错误描述。
func runCustomerMessage(ctx context.Context, db *bun.DB, enqueuer servertask.TxEnqueuer, identity *servermodels.Identity, payload customerMessagePayload, kind string) (conversationaction.ConversationMessage, error) {
	// 预生成本次发送新建记录使用的 UUIDv7。
	ids := memberMessageIDs{subject: uuid.NewV7().String(), participant: uuid.NewV7().String(), message: uuid.NewV7().String()}
	idempotencyKey := membersend.Key(identity, payload.ClientMessageID)
	var result conversationaction.ConversationMessage
	err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		var executeErr error
		result, executeErr = sendCustomerMessage(ctx, tx, identity, enqueuer, payload, ids, idempotencyKey)
		return executeErr
	})
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 发送结果与历史查询使用同一引用能力判定，未取得平台回执时不可被引用。
	replyUnavailable, err := conversationaction.MessageReplyUnavailable(ctx, db, identity, payload.ConversationID, result.ID)
	if err != nil {
		return conversationaction.ConversationMessage{}, fmt.Errorf("load sent %s reference state: %w", kind, err)
	}
	result.ReplyUnavailable = replyUnavailable
	return result, nil
}

// sendCustomerMessage 执行一次完整的成员客户会话回复事务，文本与附件共用客服周期、引用和外发语义。
func sendCustomerMessage(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, enqueuer servertask.TxEnqueuer, input customerMessagePayload, ids memberMessageIDs, idempotencyKey string) (conversationaction.ConversationMessage, error) {
	if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 发送资格校验前先按发送编号读取本人保存的消息。
	if saved, found, err := membersend.Replay(ctx, tx, identity, input.intent(), idempotencyKey); err != nil || found {
		return saved, err
	}
	// 内部备注不要求接待资格，对客回复只有开启接待的成员可以发送。
	internalNote := input.Visibility == domain.MessageVisibilityInternal
	if !internalNote {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	service, err := chatstate.LoadServiceConversation(ctx, tx, identity.Workspace.ID, input.ConversationID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 渠道投递路由只用于渠道来源的对客消息，其他来源的发起人直接在会话中读到回复。
	channelReply := !internalNote && domain.ServiceSource(service.Source) == domain.ServiceSourceChannel
	var route deliveryaction.Route
	if channelReply {
		route, err = deliveryaction.Prepare(ctx, tx, identity.Workspace.ID, input.ConversationID)
		if errors.Is(err, deliveryaction.ErrUnavailable) {
			return conversationaction.ConversationMessage{}, conversationaction.ErrConversationNotFound
		}
		if err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	conversation, err := chatstate.LockServiceConversation(ctx, tx, identity.Workspace.ID, input.ConversationID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if channelReply && !domain.ChannelCapabilitiesOf(route.ChannelType).Outbound() {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonChannelOutboundUnsupported}
	}
	session, err := chatstate.LockCurrentServiceSession(ctx, tx, identity.Workspace.ID, conversation.ID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 持有客服周期锁后复核幂等结果，再写入领取、参与者等关联记录。
	if saved, found, err := membersend.Replay(ctx, tx, identity, input.intent(), idempotencyKey); err != nil || found {
		return saved, err
	}
	// 译文按来源渠道的文本上限校验。
	if channelReply && input.Translation != nil && !domain.ChannelCapabilitiesOf(route.ChannelType).TextFits(input.Translation.Body) {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: ConflictReasonTranslationTooLong}
	}
	// 渠道来源的附件按来源渠道的附件能力和说明上限校验，附件类型与字节数在锁定文件时校验。
	var channel *domain.ChannelCapabilities
	if channelReply {
		channel = new(domain.ChannelCapabilitiesOf(route.ChannelType))
	}
	// 渠道来源的对客文本按来源渠道的单条文本字符与字节上限校验，译文另按上方规则校验。
	if channel != nil && input.Attachment == nil && input.Translation == nil && !channel.TextFits(input.Body) {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: ConflictReasonTextTooLong}
	}
	if channel != nil && input.Attachment != nil {
		if len(channel.Attachments) == 0 {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: ConflictReasonChannelAttachmentUnsupported}
		}
		if utf8.RuneCountInString(input.Body) > channel.CaptionLimit {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: ConflictReasonCaptionTooLong}
		}
	}

	if channelReply && route.Platform() {
		if !route.RecipientBound {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonChannelRecipientUnbound}
		}
		if !route.ReplyWindowOpen {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonChannelReplyWindowClosed}
		}
		if !route.Ready() {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonChannelOutboundUnavailable}
		}
		if input.ReplyToMessageID != "" && !channel.Quote {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
		}
		if input.ReplyToMessageID != "" {
			// 引用必须具有当前平台账号、同一对方编号下的确定平台消息身份，多段消息引用首段。
			var providerID string
			err := tx.NewSelect().TableExpr("channel_messages AS cm").ColumnExpr("cm.provider_message_id").
				Join("JOIN channel_identities AS ci ON ci.workspace_id = cm.workspace_id AND ci.channel_id = cm.channel_id AND ci.external_id = cm.provider_conversation_id").
				Join("JOIN messages AS msg ON msg.id = cm.message_id AND msg.workspace_id = cm.workspace_id AND msg.conversation_id = cm.conversation_id").
				Where("cm.workspace_id = ? AND cm.conversation_id = ? AND cm.message_id = ?", identity.Workspace.ID, conversation.ID, input.ReplyToMessageID).
				Where("cm.channel_id = ? AND cm.provider_account_id = ? AND ci.id = ?", route.ChannelID, *route.ProviderAccountID, route.IdentityID).
				Where("msg.type IN (?) AND msg.deleted_at IS NULL", bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
				OrderExpr("cm.part").Limit(1).Scan(ctx, &providerID)
			if errors.Is(err, sql.ErrNoRows) {
				return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
			}
			if err != nil {
				return conversationaction.ConversationMessage{}, err
			}
			route.ReplyProviderMessageID = &providerID
		}
	}
	// 消息时间取持有客服周期锁之后的数据库时刻。
	originatedAt, err := serverstorage.ClockNow(ctx, tx)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 对客回复要求周期开放，内部备注可以补记到已关闭周期。
	if !internalNote && domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusClosed {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionNotReplyable}
	}
	// 对客回复要求周期无人负责或由本人负责，内部备注对能读取该会话的成员开放。
	if !internalNote && session.AssigneeIdentityID != nil && *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
	}
	replyTo, err := conversationaction.LoadConversationReplyTarget(ctx, tx, identity.Workspace.ID, conversation.ID, input.ReplyToMessageID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 对客消息只能引用对客可见的消息。
	if !internalNote && replyTo != nil && replyTo.Visibility == domain.MessageVisibilityInternal {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
	}
	if !internalNote {
		// 回复无人负责的周期即领取该周期。
		if session.AssigneeIdentityID == nil {
			if err := servicestate.Begin(session).Assign(identity.WorkspaceIdentity.ID, originatedAt).Save(ctx, tx, enqueuer); err != nil {
				return conversationaction.ConversationMessage{}, err
			}
			if err := appendServiceSessionEvent(ctx, tx, enqueuer, identity, conversation, session, domain.ServiceSource(service.Source), domain.ConversationSystemEventServiceSessionClaimed, nil, nil); err != nil {
				return conversationaction.ConversationMessage{}, err
			}
		}
	}
	// 取得或创建发送者与提醒成员的聊天主体，再建立发送者参与者和协作者关系。
	subject, mentions, err := ensureNoteSubjects(ctx, tx, identity, ids.subject, input.MentionIdentityIDs)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 内部备注对发起人不可见，不能提醒发起人。
	for _, mention := range mentions {
		if mention.ChatSubjectID == service.RequesterSubjectID {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: ConflictReasonNoteMentionTargetInvalid}
		}
	}
	participant, err := chatstate.EnsureParticipant(ctx, tx, identity.Workspace.ID, conversation.ID, subject.ID, ids.participant)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	for _, mention := range mentions {
		if _, err := chatstate.EnsureParticipant(ctx, tx, identity.Workspace.ID, conversation.ID, mention.ChatSubjectID, uuid.NewV7().String()); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}

	message := &servermodels.Message{
		ID: ids.message, WorkspaceID: identity.Workspace.ID, ConversationID: conversation.ID,
		ServiceSessionID: &session.ID, SenderParticipantID: &participant.ID,
		Type: string(input.Type), Visibility: string(input.Visibility), Body: input.Body, ClientMessageID: &input.ClientMessageID, IdempotencyKey: &idempotencyKey, OriginatedAt: originatedAt,
	}
	// 翻译发送时客户收到译文，原文与译文都进入检索。
	if input.Translation != nil {
		message.Body, message.Language = input.Translation.Body, &input.Translation.Language
		message.SearchVector = searchtext.Vector(input.Translation.Body, input.Body)
	}
	if replyTo != nil {
		message.ReplyToMessageID = &replyTo.ID
	}
	var attachment *conversationaction.MessageAttachment
	if input.Attachment != nil {
		attachment, err = activateCustomerAttachment(ctx, tx, identity, channel, *input.Attachment)
		if err != nil {
			return conversationaction.ConversationMessage{}, err
		}
		message.SearchVector = searchtext.Vector(input.Body, attachment.Name)
	}
	message, inserted, err := chatstate.AppendServiceMessage(ctx, tx, enqueuer, conversation, session, message)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if !inserted {
		saved, _, err := membersend.Replay(ctx, tx, identity, input.intent(), idempotencyKey)
		return saved, err
	}
	if err := conversationaction.CreateMessageMentions(ctx, tx, identity.Workspace.ID, message.ID, mentions); err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if attachment != nil {
		if err := conversationaction.SaveMessageAttachment(ctx, tx, identity.Workspace.ID, message.ID, *attachment); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	// 客服书写的原文按客服语言保存为该消息的译文。
	if input.Translation != nil {
		if _, err := tx.NewInsert().Model(&servermodels.MessageTranslation{
			MessageID: message.ID, Language: input.Translation.SourceLanguage, WorkspaceID: identity.Workspace.ID, Body: input.Body, Authored: true,
		}).Exec(ctx); err != nil {
			return conversationaction.ConversationMessage{}, fmt.Errorf("save authored message translation: %w", err)
		}
	}
	if !internalNote {
		if route.Platform() {
			if err := deliveryaction.Enqueue(ctx, tx, enqueuer, route, message); err != nil {
				return conversationaction.ConversationMessage{}, err
			}
		}
		// 网站访客未读到真人回复时由邮件通知。
		if route.ChannelType == domain.ChannelTypeWebsite {
			if err := customernotify.ScheduleCheck(ctx, tx, identity.Workspace.ID, conversation.ID, originatedAt); err != nil {
				return conversationaction.ConversationMessage{}, err
			}
		}
		if err := servicestate.RecordStaffReply(ctx, tx, session, originatedAt); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	result := conversationaction.MemberConversationMessage(message, subject.ID, identity.WorkspaceIdentity)
	result.ReplyTo = replyTo
	result.Attachment = attachment
	result.Mentions = mentions
	if input.Translation != nil {
		result.Translation = &conversationaction.MessageTranslation{Language: input.Translation.SourceLanguage, Body: input.Body}
	}
	return result, nil
}

// ensureNoteSubjects 校验内部备注提醒的企业成员，按身份编号顺序取得或创建发送者与提醒成员的聊天主体，提醒按正文顺序返回。
func ensureNoteSubjects(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, senderSubjectID string, identityIDs []string) (*servermodels.ChatSubject, []conversationaction.ConversationMessageMention, error) {
	var rows []struct {
		ID          string `bun:"id"`
		DisplayName string `bun:"display_name"`
	}
	if len(identityIDs) > 0 {
		if err := tx.NewSelect().TableExpr("workspace_identities AS oi").
			ColumnExpr("oi.id, oi.display_name").
			Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
			Where("oi.workspace_id = ? AND oi.id IN (?)", identity.Workspace.ID, bun.List(identityIDs)).
			Where("oi.type = ? AND u.status = ?", domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive).
			Where("oi.id <> ?", identity.WorkspaceIdentity.ID).
			Scan(ctx, &rows); err != nil {
			return nil, nil, fmt.Errorf("load note mention targets: %w", err)
		}
		if len(rows) != len(identityIDs) {
			return nil, nil, &conversationaction.ConflictError{Reason: ConflictReasonNoteMentionTargetInvalid}
		}
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.DisplayName
	}
	// 并发互相提醒的事务按同一身份编号顺序创建聊天主体，唯一约束等待不会形成循环。
	newSubjectIDs := map[string]string{identity.WorkspaceIdentity.ID: senderSubjectID}
	for _, identityID := range identityIDs {
		newSubjectIDs[identityID] = uuid.NewV7().String()
	}
	subjects := make(map[string]*servermodels.ChatSubject, len(newSubjectIDs))
	for _, identityID := range slices.Sorted(maps.Keys(newSubjectIDs)) {
		subject, err := chatstate.EnsureSubject(ctx, tx, identity.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, identityID, newSubjectIDs[identityID])
		if err != nil {
			return nil, nil, err
		}
		subjects[identityID] = subject
	}
	mentions := make([]conversationaction.ConversationMessageMention, 0, len(identityIDs))
	for _, identityID := range identityIDs {
		name := names[identityID]
		mentions = append(mentions, conversationaction.ConversationMessageMention{
			ChatSubjectID: subjects[identityID].ID, Kind: domain.ChatSubjectKindWorkspaceIdentity,
			SourceID: identityID, DisplayName: &name, IdentityType: domain.WorkspaceIdentityTypeUser,
		})
	}
	return subjects[identity.WorkspaceIdentity.ID], mentions, nil
}

// normalizeServiceTextMessageInput 规范化成员客户消息输入，并校验提醒与译文只用于对应的消息可见范围。
func normalizeServiceTextMessageInput(input ServiceTextMessageInput) (ServiceTextMessageInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	input.Body = strings.TrimSpace(input.Body)
	input.ConversationID, _ = str.NormalizeUUID(input.ConversationID)
	input.ClientMessageID, _ = str.NormalizeUUID(input.ClientMessageID)
	if input.ReplyToMessageID != "" {
		input.ReplyToMessageID, _ = str.NormalizeUUID(input.ReplyToMessageID)
	}
	if input.Visibility == "" {
		input.Visibility = domain.MessageVisibilityShared
	}
	// 只有内部备注可以提醒企业成员，同一成员只提醒一次。
	if len(input.MentionIdentityIDs) > 0 && input.Visibility != domain.MessageVisibilityInternal {
		fields["mentionIdentityIds"] = ValidationMentionIdentityIDsInvalid
	}
	mentionIdentityIDs := make([]string, 0, len(input.MentionIdentityIDs))
	for _, identityID := range input.MentionIdentityIDs {
		if normalized, _ := str.NormalizeUUID(identityID); !slices.Contains(mentionIdentityIDs, normalized) {
			mentionIdentityIDs = append(mentionIdentityIDs, normalized)
		}
	}
	input.MentionIdentityIDs = mentionIdentityIDs
	// 译文只用于对客回复，语言标签取规范形式。
	if input.Translation != nil {
		translation := *input.Translation
		translation.Body = strings.TrimSpace(translation.Body)
		language, languageValid := languagetag.Normalize(translation.Language)
		source, sourceValid := languagetag.Normalize(translation.SourceLanguage)
		if input.Visibility != domain.MessageVisibilityShared || !languageValid || !sourceValid || translation.Body == "" || utf8.RuneCountInString(translation.Body) > 8000 {
			fields["translation"] = ValidationTranslationInvalid
		}
		translation.Language, translation.SourceLanguage = language, source
		input.Translation = &translation
	}
	return input, fields
}
