//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	"github.com/runforyou-ai/cervi/internal/actions/customernotify"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/common/customeridentity"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// inboundMessageRetryableConstraintNames 是客户入站消息事务遇到后可整体重试的唯一约束。
var inboundMessageRetryableConstraintNames = map[string]struct{}{
	"contact_channel_identities_channel_external_unique":                 {},
	"contacts_organization_external_user_unique":                         {},
	"chat_subjects_organization_kind_source_unique":                      {},
	"conversation_participants_org_conversation_subject_unique":          {},
	"service_sessions_organization_service_conversation_open_unique":     {},
	"service_sessions_organization_service_conversation_sequence_unique": {},
}

// ReceiveWebsiteCustomerMessageAction 持久化网站访客文本与附件消息。
type ReceiveWebsiteCustomerMessageAction struct {
	db             *bun.DB
	agentScheduler conversationaction.CustomerAgentMessageScheduler
	enqueuer       servertask.TxEnqueuer
	emailSender    customernotify.Sender
}

type generatedIDs struct {
	contact         string
	channelIdentity string
	subject         string
	conversation    string
	participant     string
	serviceSession  string
	message         string
}

// NewReceiveWebsiteCustomerMessageAction 创建网站访客消息操作；emailSender 为空表示部署未配置邮件发送。
func NewReceiveWebsiteCustomerMessageAction(db *bun.DB, agentScheduler conversationaction.CustomerAgentMessageScheduler, enqueuer servertask.TxEnqueuer, emailSender customernotify.Sender) *ReceiveWebsiteCustomerMessageAction {
	return &ReceiveWebsiteCustomerMessageAction{db: db, agentScheduler: agentScheduler, enqueuer: enqueuer, emailSender: emailSender}
}

// Execute 在一个可重试事务中写入网站访客文本消息。
func (a *ReceiveWebsiteCustomerMessageAction) Execute(ctx context.Context, input WebsiteCustomerTextMessageInput) (ReceiveWebsiteCustomerMessageResult, error) {
	normalized, fields := normalizeWebsiteMessageInput(input)
	if len(fields) > 0 {
		return ReceiveWebsiteCustomerMessageResult{}, &conversationaction.ValidationError{Fields: fields}
	}
	return a.receive(ctx, normalized.ChannelID, websiteInboundInput(normalized.Customer, normalized.VisitorContext, InboundCustomerMessageInput{
		ExternalID: normalized.ExternalID, RequestedConversationID: normalized.ConversationID,
		Body: normalized.Body, ClientMessageID: &normalized.ClientMessageID, ReplyToMessageID: normalized.ReplyToMessageID,
	}))
}

// ExecuteAttachment 在一个可重试事务中写入网站访客附件消息并激活上传文件。
func (a *ReceiveWebsiteCustomerMessageAction) ExecuteAttachment(ctx context.Context, input WebsiteCustomerAttachmentMessageInput) (ReceiveWebsiteCustomerMessageResult, error) {
	normalized, fields := normalizeWebsiteAttachmentMessageInput(input)
	if len(fields) > 0 {
		return ReceiveWebsiteCustomerMessageResult{}, &conversationaction.ValidationError{Fields: fields}
	}
	return a.receive(ctx, normalized.ChannelID, websiteInboundInput(normalized.Customer, normalized.VisitorContext, InboundCustomerMessageInput{
		ExternalID: normalized.ExternalID, RequestedConversationID: normalized.ConversationID,
		Body: normalized.Body, ClientMessageID: &normalized.ClientMessageID, ReplyToMessageID: normalized.ReplyToMessageID,
		Attachment: &InboundCustomerAttachment{FileID: normalized.FileID, ImageWidth: normalized.ImageWidth, ImageHeight: normalized.ImageHeight},
	}))
}

// websiteInboundInput 为网站入站消息补充签名身份与访客上下文；签名中的名称写入渠道身份显示名称，省略时不改动，带入的档案随消息写入联系人。
func websiteInboundInput(customer *WebsiteCustomer, visitorContext *domain.VisitorContext, input InboundCustomerMessageInput) InboundCustomerMessageInput {
	input.VisitorContext = visitorContext
	if customer != nil {
		input.ExternalUserID, input.Email, input.WebsiteProfile = customer.UserID, customer.Email, &customer.Profile
		if customer.Name != "" {
			input.DisplayName = &customer.Name
		}
	}
	return input
}

// receive 在可重试事务中写入访客入站消息。
func (a *ReceiveWebsiteCustomerMessageAction) receive(ctx context.Context, channelID string, input InboundCustomerMessageInput) (ReceiveWebsiteCustomerMessageResult, error) {
	var result ReceiveWebsiteCustomerMessageResult
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, inboundMessageRetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		var executeErr error
		result, executeErr = a.executeTransaction(ctx, tx, channelID, input)
		return executeErr
	})
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	// 事务提交后解析会话当前的接待状态；解析失败时消息已保存，接待状态留空，由访客端后续读取目录补齐。
	resolver := chatstate.NewReceptionResolver(a.db, result.OrganizationID, time.Now())
	if err := resolveSummaryReception(ctx, resolver, &result.Conversation); err != nil {
		slog.Warn("解析网站访客会话接待状态失败", "channel_id", channelID, "conversation_id", result.Conversation.ID, "error", err)
	}
	return result, nil
}

// executeTransaction 执行一次完整的网站访客消息事务；转人工后等待真人回复期间，从访客消息中收集接收回复的邮箱。
func (a *ReceiveWebsiteCustomerMessageAction) executeTransaction(ctx context.Context, tx bun.Tx, channelID string, input InboundCustomerMessageInput) (ReceiveWebsiteCustomerMessageResult, error) {
	channel, err := loadWebsiteChannel(ctx, tx, channelID)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	received, err := ReceiveInboundCustomerMessage(ctx, tx, a.enqueuer, channel, input)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	if !received.Inserted {
		slog.Debug("网站访客消息幂等命中",
			"channel_id", channel.ID,
			"conversation_id", received.Message.ConversationID,
			"message_id", received.Message.ID,
		)
		return receiveWebsiteCustomerMessageResult(channel.OrganizationID, received), nil
	}
	// 会话锁已由入站写入持有，这里重新读取推进后的会话版本。
	conversation, err := chatstate.LockChannelConversation(ctx, tx, channel.OrganizationID, received.Message.ConversationID)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	if _, err := customernotify.CollectEmail(ctx, tx, a.emailSender, conversation, received.Session, received.Message.Body); err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	if a.agentScheduler == nil {
		return ReceiveWebsiteCustomerMessageResult{}, errors.New("customer agent scheduler is unavailable")
	}
	if _, err := a.agentScheduler.ScheduleCustomerAuto(
		ctx, tx, channel.OrganizationID, received.Message.ConversationID, received.Session.ID, received.Message.ID,
	); err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, fmt.Errorf("schedule website customer agent: %w", err)
	}
	return receiveWebsiteCustomerMessageResult(channel.OrganizationID, received), nil
}

// normalizeWebsiteMessageInput 规范化并校验网站消息输入。
func normalizeWebsiteMessageInput(input WebsiteCustomerTextMessageInput) (WebsiteCustomerTextMessageInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	input.Body = strings.TrimSpace(input.Body)
	if !common.ValidUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) || (input.Customer != nil && input.ExternalID != customeridentity.CustomerExternalID(input.Customer.UserID)) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if input.ConversationID != nil && !common.ValidUUID(*input.ConversationID) {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	clientMessageID, valid := common.NormalizeUUID(input.ClientMessageID)
	input.ClientMessageID = clientMessageID
	if !valid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	if input.ReplyToMessageID != "" {
		var valid bool
		input.ReplyToMessageID, valid = common.NormalizeUUID(input.ReplyToMessageID)
		if !valid || input.ConversationID == nil {
			fields["replyToMessageId"] = conversationaction.ValidationReplyToMessageIDInvalid
		}
	}
	if input.Body == "" {
		fields["body"] = conversationaction.ValidationBodyRequired
	} else if utf8.RuneCountInString(input.Body) > conversationaction.MaxMessageBodyRunes {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	return input, fields
}

// normalizeWebsiteAttachmentMessageInput 规范化并校验网站访客附件消息输入。
func normalizeWebsiteAttachmentMessageInput(input WebsiteCustomerAttachmentMessageInput) (WebsiteCustomerAttachmentMessageInput, map[string]conversationaction.ValidationCode) {
	text, fields := normalizeWebsiteMessageInput(WebsiteCustomerTextMessageInput{
		ChannelID: input.ChannelID, ExternalID: input.ExternalID, ConversationID: input.ConversationID,
		ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
		Customer: input.Customer,
	})
	input.ChannelID, input.ExternalID, input.ConversationID = text.ChannelID, text.ExternalID, text.ConversationID
	input.ClientMessageID, input.Body, input.ReplyToMessageID = text.ClientMessageID, text.Body, text.ReplyToMessageID
	// 附件消息的说明可以为空，长度仍按渠道说明上限校验。
	if fields["body"] == conversationaction.ValidationBodyRequired {
		delete(fields, "body")
	}
	if utf8.RuneCountInString(input.Body) > domain.ChannelCaptionLimit(domain.ChannelTypeWebsite) {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	var valid bool
	input.FileID, valid = common.NormalizeUUID(input.FileID)
	if !valid || input.ImageWidth < 0 || input.ImageHeight < 0 {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	return input, fields
}

// generateIDs 为一次渠道消息写入生成 UUIDv7。
func generateIDs() generatedIDs {
	values := make([]string, 7)
	for index := range values {
		values[index] = uuid.NewV7().String()
	}
	return generatedIDs{
		contact: values[0], channelIdentity: values[1], subject: values[2], conversation: values[3],
		participant: values[4], serviceSession: values[5], message: values[6],
	}
}

// loadWebsiteChannel 读取启用的网站渠道和路由配置。
func loadWebsiteChannel(ctx context.Context, db bun.IDB, channelID string) (*servermodels.Channel, error) {
	channel := &servermodels.Channel{}
	err := db.NewSelect().Model(channel).
		Where("c.id = ?", channelID).
		Where("c.type = ?", domain.ChannelTypeWebsite).
		Where("c.enabled = TRUE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, conversationaction.ErrChannelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load website channel: %w", err)
	}
	return channel, nil
}

// loadWebsiteVisitorIdentity 读取网站渠道内当前访客的渠道身份，尚未建立身份时返回 false；读操作不创建联系人。
func loadWebsiteVisitorIdentity(ctx context.Context, db bun.IDB, channel *servermodels.Channel, externalID string) (*servermodels.ContactChannelIdentity, bool, error) {
	identity := &servermodels.ContactChannelIdentity{}
	err := db.NewSelect().Model(identity).
		Where("cci.organization_id = ?", channel.OrganizationID).
		Where("cci.channel_id = ?", channel.ID).
		Where("cci.external_id = ?", externalID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load website visitor identity: %w", err)
	}
	return identity, true, nil
}

// ensureContactSubject 取得或创建联系人聊天主体。
func ensureContactSubject(ctx context.Context, db bun.IDB, organizationID, contactID, subjectID string) (*servermodels.ChatSubject, error) {
	subject := &servermodels.ChatSubject{}
	err := db.NewSelect().Model(subject).
		Where("cs.organization_id = ?", organizationID).
		Where("cs.kind = ?", domain.ChatSubjectKindContact).
		Where("cs.source_id = ?", contactID).
		Scan(ctx)
	if err == nil {
		return subject, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find contact chat subject: %w", err)
	}
	subject = &servermodels.ChatSubject{ID: subjectID, OrganizationID: organizationID, Kind: string(domain.ChatSubjectKindContact), SourceID: contactID}
	if _, err := db.NewInsert().Model(subject).
		Column("id", "organization_id", "kind", "source_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create contact chat subject: %w", err)
	}
	return subject, nil
}

// selectTargetConversation 取得指定渠道会话或创建新的渠道会话。
func selectTargetConversation(ctx context.Context, db bun.IDB, organizationID, channelIdentityID, requesterSubjectID string, requestedConversationID *string, body, conversationID string) (*servermodels.Conversation, bool, error) {
	if requestedConversationID != nil {
		conversation := &servermodels.Conversation{}
		err := db.NewSelect().Model(conversation).
			Join("JOIN channel_conversations AS cc ON cc.organization_id = cv.organization_id AND cc.conversation_id = cv.id").
			Where("cv.organization_id = ?", organizationID).
			Where("cv.id = ?", *requestedConversationID).
			Where("cv.type = ?", domain.ConversationTypeChannel).
			Where("cc.contact_channel_identity_id = ?", channelIdentityID).
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, conversationaction.ErrConversationNotFound
		}
		if err != nil {
			return nil, false, fmt.Errorf("load customer conversation: %w", err)
		}
		return conversation, false, nil
	}

	return createChannelConversation(ctx, db, organizationID, channelIdentityID, requesterSubjectID, body, conversationID)
}

// createChannelConversation 创建渠道会话、渠道身份关系和以联系人为发起人的客户服务会话。
func createChannelConversation(ctx context.Context, db bun.IDB, organizationID, channelIdentityID, requesterSubjectID, body, conversationID string) (*servermodels.Conversation, bool, error) {
	// 从首条正文派生稳定会话标题。
	value := strings.Join(strings.Fields(body), " ")
	runes := []rune(value)
	if len(runes) > 60 {
		runes = runes[:60]
	}
	title := string(runes)
	conversation := &servermodels.Conversation{
		ID: conversationID, OrganizationID: organizationID, Type: string(domain.ConversationTypeChannel),
		Status: string(domain.ConversationStatusActive), Title: &title,
	}
	if _, err := db.NewInsert().Model(conversation).
		Column("id", "organization_id", "type", "status", "title", "created_by_subject_id").
		Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("create channel conversation: %w", err)
	}
	relation := &servermodels.ChannelConversation{ConversationID: conversation.ID, OrganizationID: organizationID, ContactChannelIdentityID: channelIdentityID}
	if _, err := db.NewInsert().Model(relation).
		Column("conversation_id", "organization_id", "contact_channel_identity_id").
		Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("create channel conversation relation: %w", err)
	}
	if err := chatstate.CreateServiceConversation(ctx, db, &servermodels.ServiceConversation{
		OrganizationID: organizationID, ConversationID: conversation.ID,
		Source: string(domain.ServiceSourceChannel), RequesterSubjectID: requesterSubjectID,
		Audience: string(domain.ServiceAudienceCustomer),
	}); err != nil {
		return nil, false, err
	}
	return conversation, true, nil
}

// ensureContactParticipant 取得或恢复联系人参与者。
func ensureContactParticipant(ctx context.Context, db bun.IDB, organizationID, conversationID, subjectID, participantID string) (*servermodels.ConversationParticipant, error) {
	participant := &servermodels.ConversationParticipant{}
	err := db.NewSelect().Model(participant).
		Where("cp.organization_id = ?", organizationID).
		Where("cp.conversation_id = ?", conversationID).
		Where("cp.subject_id = ?", subjectID).
		Scan(ctx)
	if err == nil {
		if participant.LeftAt != nil {
			if _, err := db.NewUpdate().Model(participant).
				Set("left_at = NULL").
				Set("updated_at = now()").
				WherePK().
				Where("organization_id = ?", organizationID).
				Exec(ctx); err != nil {
				return nil, fmt.Errorf("restore contact conversation participant: %w", err)
			}
			participant.LeftAt = nil
		}
		return participant, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find contact conversation participant: %w", err)
	}
	participant = &servermodels.ConversationParticipant{
		ID: participantID, OrganizationID: organizationID, ConversationID: conversationID,
		SubjectID: subjectID, Role: string(domain.ConversationParticipantRoleMember),
	}
	if _, err := db.NewInsert().Model(participant).
		Column("id", "organization_id", "conversation_id", "subject_id", "role").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create contact conversation participant: %w", err)
	}
	return participant, nil
}

// receiveWebsiteCustomerMessageResult 转换网站访客消息写入结果。
func receiveWebsiteCustomerMessageResult(organizationID string, received InboundCustomerMessageResult) ReceiveWebsiteCustomerMessageResult {
	var replyTo *MessageReference
	if reference := received.ReplyTo; reference != nil {
		replyTo = &MessageReference{ID: reference.ID, Deleted: reference.Deleted}
		if !reference.Deleted {
			replyTo.Author = domain.MessageAuthorAgent
			if reference.Sender.Kind == domain.ChatSubjectKindContact {
				replyTo.Author = domain.MessageAuthorVisitor
			}
			replyTo.Body = reference.Body
			replyTo.SenderIdentityType = reference.Sender.IdentityType
		}
	}
	return ReceiveWebsiteCustomerMessageResult{
		OrganizationID:          organizationID,
		Conversation:            received.Summary,
		CreatedConversation:     received.CreatedConversation,
		OpenedNewServiceSession: received.OpenedServiceSession,
		Message: Message{
			ClientMessageID: received.Message.ClientMessageID,
			ReplyTo:         replyTo,
			Attachment:      received.Attachment,
			ID:              received.Message.ID, Author: domain.MessageAuthorVisitor,
			MessageSeq: received.Message.MessageSeq, Body: received.Message.Body, OriginatedAt: received.Message.OriginatedAt,
			CreatedAt: received.Message.CreatedAt,
		},
	}
}

// loadConversationSummary 读取客户线程当前最后消息和当前客服周期摘要。
func loadConversationSummary(ctx context.Context, db bun.IDB, organizationID, conversationID, channelIdentityID string) (ConversationSummary, error) {
	row := conversationSummaryRow{}
	err := db.NewSelect().
		TableExpr("conversations AS cv").
		ColumnExpr("cv.id AS id").
		ColumnExpr("cv.title AS title").
		ColumnExpr("msg.originated_at AS last_message_at").
		ColumnExpr("msg.message_seq AS last_message_seq").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("current.id AS service_session_id").
		ColumnExpr("current.status AS service_session_status").
		ColumnExpr("current.team_id AS service_session_team_id").
		ColumnExpr("current.assignee_identity_id AS service_session_assignee_id").
		Join(`JOIN LATERAL (
 SELECT visible.* FROM messages AS visible
 WHERE visible.organization_id = cv.organization_id AND visible.conversation_id = cv.id AND visible.type IN (?, ?) AND visible.visibility = ? AND visible.deleted_at IS NULL
 ORDER BY visible.message_seq DESC LIMIT 1
 ) AS msg ON TRUE`, domain.MessageTypeText, domain.MessageTypeAttachment, domain.MessageVisibilityShared).
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.organization_id = msg.organization_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.organization_id = preview_cp.organization_id").
		Join("LEFT JOIN organization_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.organization_id = preview_cs.organization_id AND preview_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN channel_conversations AS cc ON cc.organization_id = cv.organization_id AND cc.conversation_id = cv.id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS current ON current.organization_id = svc.organization_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id").
		Where("cv.organization_id = ?", organizationID).
		Where("cv.id = ?", conversationID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived).
		Where("cc.contact_channel_identity_id = ?", channelIdentityID).
		Scan(ctx, &row)
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("load customer conversation summary: %w", err)
	}
	return conversationSummaryFromRow(row), nil
}
