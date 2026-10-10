//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ReceiveWebsiteCustomerMessageAction 持久化网站访客文本与附件消息。
type ReceiveWebsiteCustomerMessageAction struct {
	db             *bun.DB
	agentScheduler conversationaction.CustomerAgentMessageScheduler
	enqueuer       servertask.TxEnqueuer
	emailSender    customernotify.Sender
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
	return a.receive(ctx, normalized.ChannelID, websiteInboundInput(normalized.Customer, normalized.VisitorContext, channelinbound.Input{
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
	return a.receive(ctx, normalized.ChannelID, websiteInboundInput(normalized.Customer, normalized.VisitorContext, channelinbound.Input{
		ExternalID: normalized.ExternalID, RequestedConversationID: normalized.ConversationID,
		Body: normalized.Body, ClientMessageID: &normalized.ClientMessageID, ReplyToMessageID: normalized.ReplyToMessageID,
		Attachment: &channelinbound.UploadedFile{FileID: normalized.FileID, ImageWidth: normalized.ImageWidth, ImageHeight: normalized.ImageHeight},
	}))
}

// websiteInboundInput 为网站入站消息补充签名身份与访客上下文；网站请求自身给出核验结论，签名中的名称写入渠道身份显示名称，省略时不改动，带入的档案随消息写入联系人。
func websiteInboundInput(customer *SignedCustomer, visitorContext *domain.VisitorContext, input channelinbound.Input) channelinbound.Input {
	input.VisitorContext = visitorContext
	input.IdentityAsserted = true
	if customer != nil {
		input.VerifiedUserID, input.Email, input.SignedProfile = customer.UserID, customer.Email, &customer.Profile
		if customer.Name != "" {
			input.DisplayName = &customer.Name
		}
	}
	return input
}

// receive 在可重试事务中写入访客入站消息。
func (a *ReceiveWebsiteCustomerMessageAction) receive(ctx context.Context, channelID string, input channelinbound.Input) (ReceiveWebsiteCustomerMessageResult, error) {
	var result ReceiveWebsiteCustomerMessageResult
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, channelinbound.RetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		var executeErr error
		result, executeErr = a.executeTransaction(ctx, tx, channelID, input)
		return executeErr
	})
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	// 事务提交后解析会话当前的接待状态；解析失败时消息已保存，接待状态留空，由访客端后续读取目录补齐。
	resolver := serviceroute.NewReceptionResolver(a.db, result.WorkspaceID)
	if err := resolveSummaryReception(ctx, resolver, &result.Conversation); err != nil {
		slog.WarnContext(ctx, "解析网站访客会话接待状态失败", "channel_id", channelID, "conversation_id", result.Conversation.ID, "error", err)
	}
	return result, nil
}

// executeTransaction 按渠道的附件与多会话设置执行一次完整的网站访客消息事务；转人工后等待真人回复期间，从访客消息中收集接收回复的邮箱。
func (a *ReceiveWebsiteCustomerMessageAction) executeTransaction(ctx context.Context, tx bun.Tx, channelID string, input channelinbound.Input) (ReceiveWebsiteCustomerMessageResult, error) {
	channel, err := loadWebsiteChannel(ctx, tx, channelID)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	setting, err := loadWebsiteChannelSetting(ctx, tx, channel)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	if input.Attachment != nil && !setting.AttachmentsEnabled {
		return ReceiveWebsiteCustomerMessageResult{}, &conversationaction.ConflictError{Reason: ConflictReasonAttachmentsDisabled}
	}
	// 未开启多会话时，未指定会话的消息进入访客最近有消息的会话。
	input.SingleConversation = !setting.MultipleConversationsEnabled && input.RequestedConversationID == nil
	received, err := channelinbound.Receive(ctx, tx, a.enqueuer, channel, input)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
	if !received.Inserted {
		slog.DebugContext(ctx, "网站访客消息幂等命中",
			"channel_id", channel.ID,
			"conversation_id", received.Message.ConversationID,
			"message_id", received.Message.ID,
		)
		return receiveWebsiteCustomerMessageResult(ctx, tx, channel.WorkspaceID, received)
	}
	// 会话锁已由入站写入持有，这里重新读取推进后的会话版本。
	conversation, err := chatstate.LockChannelConversation(ctx, tx, channel.WorkspaceID, received.Message.ConversationID)
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
		ctx, tx, channel.WorkspaceID, received.Message.ConversationID, received.Session.ID, received.Message.ID,
	); err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, fmt.Errorf("schedule website customer agent: %w", err)
	}
	return receiveWebsiteCustomerMessageResult(ctx, tx, channel.WorkspaceID, received)
}

// normalizeWebsiteMessageInput 规范化并校验网站消息输入。
func normalizeWebsiteMessageInput(input WebsiteCustomerTextMessageInput) (WebsiteCustomerTextMessageInput, map[string]conversationaction.ValidationCode) {
	fields := map[string]conversationaction.ValidationCode{}
	input.Body = strings.TrimSpace(input.Body)
	if !str.IsUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) || (input.Customer != nil && input.ExternalID != customeridentity.CustomerExternalID(input.Customer.UserID)) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if input.ConversationID != nil && !str.IsUUID(*input.ConversationID) {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	clientMessageID, valid := str.NormalizeUUID(input.ClientMessageID)
	input.ClientMessageID = clientMessageID
	if !valid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	if input.ReplyToMessageID != "" {
		var valid bool
		input.ReplyToMessageID, valid = str.NormalizeUUID(input.ReplyToMessageID)
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
	if utf8.RuneCountInString(input.Body) > domain.ChannelCapabilitiesOf(domain.ChannelTypeWebsite).CaptionLimit {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	var valid bool
	input.FileID, valid = str.NormalizeUUID(input.FileID)
	if !valid || input.ImageWidth < 0 || input.ImageHeight < 0 {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	return input, fields
}

// loadWebsiteChannel 读取接待客户的网站渠道和路由配置。
func loadWebsiteChannel(ctx context.Context, db bun.IDB, channelID string) (*servermodels.Channel, error) {
	channel := &servermodels.Channel{}
	err := db.NewSelect().Model(channel).
		Where("c.id = ?", channelID).
		Where("c.type = ?", domain.ChannelTypeWebsite).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChannelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load website channel: %w", err)
	}
	return channel, nil
}

// loadWebsiteChannelSetting 读取网站渠道的访客聊天界面设置。
func loadWebsiteChannelSetting(ctx context.Context, db bun.IDB, channel *servermodels.Channel) (*servermodels.WebsiteChannelSetting, error) {
	setting := &servermodels.WebsiteChannelSetting{}
	if err := db.NewSelect().Model(setting).
		Where("wcs.workspace_id = ? AND wcs.channel_id = ?", channel.WorkspaceID, channel.ID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load website channel setting: %w", err)
	}
	return setting, nil
}

// loadWebsiteVisitorIdentity 读取网站渠道内当前访客的渠道身份，尚未建立身份时返回 false；读操作不创建联系人。
func loadWebsiteVisitorIdentity(ctx context.Context, db bun.IDB, channel *servermodels.Channel, externalID string) (*servermodels.ChannelIdentity, bool, error) {
	identity := &servermodels.ChannelIdentity{}
	err := db.NewSelect().Model(identity).
		Where("ci.workspace_id = ?", channel.WorkspaceID).
		Where("ci.channel_id = ?", channel.ID).
		Where("ci.external_id = ?", externalID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load website visitor identity: %w", err)
	}
	return identity, true, nil
}

// receiveWebsiteCustomerMessageResult 读取会话摘要并转换网站访客消息写入结果。
func receiveWebsiteCustomerMessageResult(ctx context.Context, db bun.IDB, workspaceID string, received channelinbound.Result) (ReceiveWebsiteCustomerMessageResult, error) {
	summary, err := loadConversationSummary(ctx, db, workspaceID, received.Message.ConversationID, received.ChannelIdentityID)
	if err != nil {
		return ReceiveWebsiteCustomerMessageResult{}, err
	}
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
		WorkspaceID:             workspaceID,
		Conversation:            summary,
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
	}, nil
}

// loadConversationSummary 读取客户线程当前最后消息和当前客服周期摘要。
func loadConversationSummary(ctx context.Context, db bun.IDB, workspaceID, conversationID, channelIdentityID string) (ConversationSummary, error) {
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
 WHERE visible.workspace_id = cv.workspace_id AND visible.conversation_id = cv.id AND visible.type IN (?, ?) AND visible.visibility = ? AND visible.deleted_at IS NULL
 ORDER BY visible.message_seq DESC LIMIT 1
 ) AS msg ON TRUE`, domain.MessageTypeText, domain.MessageTypeAttachment, domain.MessageVisibilityShared).
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.workspace_id = msg.workspace_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = cv.workspace_id AND cc.conversation_id = cv.id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS current ON current.workspace_id = svc.workspace_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id").
		Where("cv.workspace_id = ?", workspaceID).
		Where("cv.id = ?", conversationID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived).
		Where("cc.channel_identity_id = ?", channelIdentityID).
		Scan(ctx, &row)
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("load customer conversation summary: %w", err)
	}
	return conversationSummaryFromRow(row), nil
}
