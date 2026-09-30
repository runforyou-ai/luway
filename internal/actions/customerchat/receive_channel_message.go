//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/uptrace/bun"
)

// InboundCustomerMessageInput 定义渠道文本与附件入站事务的稳定事实。
type InboundCustomerMessageInput struct {
	ChannelMessage          *channelmessage.Inbound
	ClientMessageID         *string
	Attachment              *InboundCustomerAttachment
	ExternalMedia           *InboundExternalMedia
	ReplyToMessageID        string
	ExternalID              string
	ExternalUserID          string
	Email                   string
	WebsiteProfile          *domain.WebsiteContactProfile
	VisitorContext          *domain.VisitorContext
	DisplayName             *string
	RequestedConversationID *string
	SingleConversation      bool
	Body                    string
	IdempotencyKey          string
	OriginatedAt            time.Time
	SourceOrder             int64
}

// InboundCustomerAttachment 定义客户入站附件消息待关联的已上传文件。
type InboundCustomerAttachment struct {
	FileID      string
	ImageWidth  int
	ImageHeight int
}

// InboundExternalMedia 定义外部平台随消息送达、内容尚待取回的媒体附件。
type InboundExternalMedia struct {
	ExternalID     string
	FileName       string
	ContentType    string
	ByteSize       int64
	ImageWidth     int
	ImageHeight    int
	StorageBackend domain.FileStorageBackend
}

// messageType 返回本次入站写入的消息类型。
func (i InboundCustomerMessageInput) messageType() domain.MessageType {
	if i.Attachment != nil || i.ExternalMedia != nil {
		return domain.MessageTypeAttachment
	}
	return domain.MessageTypeText
}

// InboundCustomerMessageResult 返回渠道入站事务创建或取得的事实。
type InboundCustomerMessageResult struct {
	ReplyTo              *conversationaction.ConversationMessageReference
	Attachment           *VisitorAttachment
	Summary              ConversationSummary
	Session              *servermodels.ServiceSession
	Message              *servermodels.Message
	ChannelIdentityID    string
	Inserted             bool
	CreatedConversation  bool
	OpenedServiceSession bool
}

// errNewSessionRouteRequired 表示本次写入需要开启新客服处理周期，须先锁定路由目标再重做。
var errNewSessionRouteRequired = errors.New("new service session route required")

// ReceiveInboundCustomerMessage 在调用方事务中幂等写入客户文本或附件消息；新客服处理周期路由到队列时投递分配任务，路由目标先于渠道身份与会话取共享锁。
func ReceiveInboundCustomerMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, channel *servermodels.Channel, input InboundCustomerMessageInput) (InboundCustomerMessageResult, error) {
	ids := generateIDs()
	// 不加锁预判目标会话是否已有进行中周期：单会话渠道取该渠道身份最早的会话，网站渠道取访客指定的会话，未指定时新建会话。
	open := false
	if input.SingleConversation || input.RequestedConversationID != nil {
		query := db.NewSelect().TableExpr("contact_channel_identities AS cci").
			Join("JOIN channel_conversations AS cc ON cc.organization_id = cci.organization_id AND cc.contact_channel_identity_id = cci.id").
			Join("JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id").
			Join("JOIN service_sessions AS ss ON ss.organization_id = svc.organization_id AND ss.id = svc.current_service_session_id").
			Where("cci.organization_id = ? AND cci.channel_id = ? AND cci.external_id = ?", channel.OrganizationID, channel.ID, input.ExternalID).
			Where("ss.status = ?", domain.ServiceSessionStatusOpen)
		if input.RequestedConversationID != nil {
			query = query.Where("cc.conversation_id = ?", *input.RequestedConversationID)
		} else {
			query = query.Where(`cc.conversation_id = (SELECT first.conversation_id FROM channel_conversations AS first
				WHERE first.organization_id = cci.organization_id AND first.contact_channel_identity_id = cci.id
				ORDER BY first.created_at ASC, first.conversation_id ASC LIMIT 1)`)
		}
		var err error
		if open, err = query.Exists(ctx); err != nil {
			return InboundCustomerMessageResult{}, fmt.Errorf("check open service session: %w", err)
		}
	}
	if !open {
		route, err := chatstate.ResolveNewSessionRoute(ctx, db, channel)
		if err != nil {
			return InboundCustomerMessageResult{}, err
		}
		return receiveInboundCustomerMessage(ctx, db, enqueuer, channel, input, ids, &route)
	}
	if _, err := db.ExecContext(ctx, "SAVEPOINT inbound_customer_message"); err != nil {
		return InboundCustomerMessageResult{}, fmt.Errorf("create inbound message savepoint: %w", err)
	}
	result, err := receiveInboundCustomerMessage(ctx, db, enqueuer, channel, input, ids, nil)
	if !errors.Is(err, errNewSessionRouteRequired) {
		return result, err
	}
	// 预判后周期已关闭时回滚到保存点，锁定路由目标后重做写入。
	if _, err := db.ExecContext(ctx, "ROLLBACK TO SAVEPOINT inbound_customer_message"); err != nil {
		return InboundCustomerMessageResult{}, fmt.Errorf("rollback inbound message savepoint: %w", err)
	}
	route, err := chatstate.ResolveNewSessionRoute(ctx, db, channel)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	return receiveInboundCustomerMessage(ctx, db, enqueuer, channel, input, ids, &route)
}

// receiveInboundCustomerMessage 执行一次入站写入；route 为空且需要开启新周期时返回 errNewSessionRouteRequired。
func receiveInboundCustomerMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, channel *servermodels.Channel, input InboundCustomerMessageInput, ids generatedIDs, route *chatstate.RouteSnapshot) (InboundCustomerMessageResult, error) {
	ensured, err := contactaction.EnsureChannelIdentity(ctx, db, contactaction.EnsureChannelIdentityInput{
		OrganizationID: channel.OrganizationID,
		ChannelID:      channel.ID,
		ExternalID:     input.ExternalID,
		ContactID:      ids.contact,
		IdentityID:     ids.channelIdentity,
		ExternalUserID: input.ExternalUserID,
		Email:          input.Email,
	})
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	identity := ensured.Identity
	// 网站发送编号按渠道访客身份隔离，外部平台保留来源幂等键。
	if input.ClientMessageID != nil {
		input.IdempotencyKey = "chmsg:" + identity.ID + ":" + *input.ClientMessageID
	}
	// 渠道身份名称变化时同时推进其所在客户会话的版本，重放与幂等冲突同样覆盖。
	if input.DisplayName != nil && (identity.DisplayName == nil || *identity.DisplayName != *input.DisplayName) {
		if _, err := db.NewUpdate().Model(identity).
			Set("display_name = ?", *input.DisplayName).
			Set("updated_at = now()").
			WherePK().
			Where("organization_id = ?", channel.OrganizationID).
			Exec(ctx); err != nil {
			return InboundCustomerMessageResult{}, fmt.Errorf("update channel identity display name: %w", err)
		}
		identity.DisplayName = input.DisplayName
		if err := chatstate.TouchChannelIdentityConversations(ctx, db, channel.OrganizationID, identity.ID); err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	if saved, found, err := loadInboundCustomerMessage(ctx, db, channel, identity, input); err != nil || found {
		return saved, err
	}
	// 网站带入的档案只随新写入的消息更新，重放不再写入。
	if input.WebsiteProfile != nil {
		if err := contactprofileaction.ApplyWebsiteProfile(ctx, db, channel.OrganizationID, ensured.Contact.ID, *input.WebsiteProfile); err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}

	// 访客上传的附件在会话之前锁定并激活，外部媒体先建立取回中的文件记录；文件名用于新会话标题和检索向量。
	var attachment *VisitorAttachment
	titleSource := input.Body
	if input.Attachment != nil {
		attachment, err = lockInboundAttachmentFile(ctx, db, channel, identity.ID, *input.Attachment)
		if err != nil {
			return InboundCustomerMessageResult{}, err
		}
	} else if input.ExternalMedia != nil {
		attachment, err = createExternalMediaAttachment(ctx, db, channel, *input.ExternalMedia)
		if err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	if attachment != nil && titleSource == "" {
		titleSource = attachment.Name
	}

	subject, err := ensureContactSubject(ctx, db, channel.OrganizationID, ensured.Contact.ID, ids.subject)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	var conversation *servermodels.Conversation
	var insertedConversation bool
	if input.SingleConversation {
		conversation, insertedConversation, err = selectSingleChannelConversation(ctx, db, channel.OrganizationID, identity.ID, subject.ID, titleSource, ids.conversation)
	} else {
		conversation, insertedConversation, err = selectTargetConversation(ctx, db, channel.OrganizationID, identity.ID, subject.ID, input.RequestedConversationID, titleSource, ids.conversation)
	}
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	// 渠道身份稳定后锁定会话，已有线程随后锁当前周期并核对渠道身份。
	conversation, err = chatstate.LockChannelConversation(ctx, db, channel.OrganizationID, conversation.ID)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	replyTo, err := conversationaction.LoadConversationReplyTarget(ctx, db, channel.OrganizationID, conversation.ID, input.ReplyToMessageID)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	// 客户只能引用对客可见的消息。
	if replyTo != nil && replyTo.Visibility == domain.MessageVisibilityInternal {
		return InboundCustomerMessageResult{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
	}

	// 新会话尚无周期；已有会话锁定服务会话并取进行中的当前周期。
	var session *servermodels.ServiceSession
	if !insertedConversation {
		session, err = chatstate.LockOpenServiceSession(ctx, db, channel.OrganizationID, conversation.ID)
		if err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	// 网站消息在确定写入周期后生成时间，已有会话此时已持有周期锁；外部渠道保留来源时间。
	if input.OriginatedAt.IsZero() {
		input.OriginatedAt = time.Now().UTC()
	}
	if conversation.Status == string(domain.ConversationStatusArchived) {
		if _, err := db.NewUpdate().Model(conversation).
			Set("status = ?", domain.ConversationStatusActive).
			Set("updated_at = now()").
			WherePK().
			Where("organization_id = ?", channel.OrganizationID).
			Exec(ctx); err != nil {
			return InboundCustomerMessageResult{}, fmt.Errorf("reactivate customer conversation: %w", err)
		}
		conversation.Status = string(domain.ConversationStatusActive)
	}

	// 追加消息之外的会话变化类别：新周期改变服务周期与访客资料，访客上下文变化改变访客资料。
	var changes domain.ConversationChanges
	if session == nil {
		if route == nil {
			return InboundCustomerMessageResult{}, errNewSessionRouteRequired
		}
		changes = domain.ConversationChangeService | domain.ConversationChangeParticipants
		// 路由到 AI 员工时记为其接待。
		var agentIdentityID *string
		if route.AssigneeType == domain.OrganizationIdentityTypeAgent {
			agentIdentityID = route.AssigneeIdentityID
		}
		session, err = chatstate.OpenServiceSession(ctx, db, channel.OrganizationID, conversation.ID, chatstate.OpenServiceSessionInput{
			ID: ids.serviceSession, OpeningMessageID: ids.message, OpenedAt: input.OriginatedAt,
			TeamID: route.TeamID, AssigneeIdentityID: route.AssigneeIdentityID, AgentIdentityID: agentIdentityID, VisitorContext: input.VisitorContext,
		})
		if err != nil {
			return InboundCustomerMessageResult{}, err
		}
		if route.AssigneeIdentityID == nil {
			if err := serviceassignment.EnqueueAssign(ctx, db, enqueuer, serviceassignment.AssignInput{
				OrganizationID: channel.OrganizationID, ServiceSessionID: session.ID,
			}); err != nil {
				return InboundCustomerMessageResult{}, err
			}
		}
	} else if input.VisitorContext != nil {
		// 访客上下文随进行中周期的消息更新，来源页保留周期开始时的记录。
		visitorContext := *input.VisitorContext
		visitorContext.ReferrerURL = ""
		if session.VisitorContext != nil {
			visitorContext.ReferrerURL = session.VisitorContext.ReferrerURL
		}
		if session.VisitorContext == nil || *session.VisitorContext != visitorContext {
			session.VisitorContext = &visitorContext
			if _, err := db.NewUpdate().Model(session).
				Column("visitor_context").
				WherePK().
				Where("organization_id = ?", channel.OrganizationID).
				Exec(ctx); err != nil {
				return InboundCustomerMessageResult{}, fmt.Errorf("update service session visitor context: %w", err)
			}
			changes = domain.ConversationChangeParticipants
		}
	}

	participant, err := ensureContactParticipant(ctx, db, channel.OrganizationID, conversation.ID, subject.ID, ids.participant)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}

	message := &servermodels.Message{
		ID: ids.message, OrganizationID: channel.OrganizationID,
		ConversationID: conversation.ID, ServiceSessionID: &session.ID,
		SenderParticipantID: &participant.ID, Type: string(input.messageType()),
		ClientMessageID: input.ClientMessageID, Body: input.Body, IdempotencyKey: &input.IdempotencyKey,
		OriginatedAt: input.OriginatedAt, SourceOrder: input.SourceOrder,
	}
	if replyTo != nil {
		message.ReplyToMessageID = &replyTo.ID
	}
	if attachment != nil {
		message.SearchVector = searchtext.Vector(input.Body, attachment.Name)
	}
	message, inserted, err := chatstate.AppendMessage(ctx, db, conversation, message)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	if !inserted {
		saved, _, err := loadInboundCustomerMessage(ctx, db, channel, identity, input)
		return saved, err
	}
	if changes != 0 {
		if err := chatstate.NotifyConversationChanged(ctx, db, conversation, changes); err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	if attachment != nil {
		if err := conversationaction.SaveCustomerAttachment(ctx, db, channel.OrganizationID, message.ID, attachment.MessageAttachment); err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	if input.ChannelMessage != nil {
		if err := channelmessage.RecordInbound(ctx, db, channel.ID, message, input.ChannelMessage); err != nil {
			return InboundCustomerMessageResult{}, err
		}
	}
	if _, err := db.NewUpdate().Model(identity).
		Set("last_seen_at = CASE WHEN last_seen_at IS NULL OR last_seen_at < ? THEN ? ELSE last_seen_at END", input.OriginatedAt, input.OriginatedAt).
		Set("updated_at = now()").
		WherePK().
		Where("organization_id = ?", channel.OrganizationID).
		Exec(ctx); err != nil {
		return InboundCustomerMessageResult{}, fmt.Errorf("update channel identity last seen: %w", err)
	}

	summary, err := loadConversationSummary(ctx, db, channel.OrganizationID, conversation.ID, identity.ID)
	if err != nil {
		return InboundCustomerMessageResult{}, err
	}
	result := inboundCustomerMessageResult(summary, session, identity.ID, message, true)
	result.ReplyTo = replyTo
	result.Attachment = attachment
	return result, nil
}

// loadInboundCustomerMessage 校验并返回已经写入的渠道消息。
func loadInboundCustomerMessage(ctx context.Context, db bun.IDB, channel *servermodels.Channel, identity *servermodels.ContactChannelIdentity, input InboundCustomerMessageInput) (InboundCustomerMessageResult, bool, error) {
	message := &servermodels.Message{}
	err := db.NewSelect().Model(message).
		Where("msg.organization_id = ?", channel.OrganizationID).
		Where("msg.idempotency_key = ?", input.IdempotencyKey).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return InboundCustomerMessageResult{}, false, nil
	}
	if err != nil {
		return InboundCustomerMessageResult{}, false, fmt.Errorf("find idempotent inbound customer message: %w", err)
	}
	if message.ServiceSessionID == nil || message.SenderParticipantID == nil || message.DeletedAt != nil {
		return InboundCustomerMessageResult{}, true, conversationaction.ErrDataInvariant
	}
	if message.Type != string(input.messageType()) {
		return InboundCustomerMessageResult{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	var attachment *VisitorAttachment
	if input.Attachment != nil || input.ExternalMedia != nil {
		attachment = &VisitorAttachment{}
		if err := db.NewSelect().TableExpr("message_attachments AS ma").
			ColumnExpr("COALESCE(ma.file_id::text, '') AS id").
			ColumnExpr("ma.name, ma.content_type, ma.byte_size, ma.image_width, ma.image_height, ma.transfer_status").
			ColumnExpr("f.storage_backend, f.storage_key").
			Join("LEFT JOIN files AS f ON f.id = ma.file_id AND f.organization_id = ma.organization_id").
			Where("ma.message_id = ? AND ma.organization_id = ?", message.ID, channel.OrganizationID).
			Scan(ctx, attachment); err != nil {
			return InboundCustomerMessageResult{}, true, fmt.Errorf("load idempotent inbound attachment: %w", err)
		}
		// 访客附件核对文件编号，外部媒体的文件记录随取回结果变化，只核对文件名、类型与图片尺寸。
		if input.Attachment != nil && (attachment.ID != input.Attachment.FileID || attachment.ImageWidth != input.Attachment.ImageWidth || attachment.ImageHeight != input.Attachment.ImageHeight) {
			return InboundCustomerMessageResult{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
		if media := input.ExternalMedia; media != nil && (attachment.Name != media.FileName || attachment.ContentType != media.ContentType || attachment.ImageWidth != media.ImageWidth || attachment.ImageHeight != media.ImageHeight) {
			return InboundCustomerMessageResult{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
	}
	storedReply := ""
	if message.ReplyToMessageID != nil {
		storedReply = *message.ReplyToMessageID
	}
	replyMatches := storedReply == input.ReplyToMessageID
	if input.ChannelMessage != nil {
		replyMatches, err = channelmessage.MatchesInbound(ctx, db, message, channel.ID, input.ChannelMessage)
		if err != nil {
			return InboundCustomerMessageResult{}, true, err
		}
	}
	if !replyMatches || message.Body != input.Body || (input.RequestedConversationID != nil && *input.RequestedConversationID != message.ConversationID) {
		return InboundCustomerMessageResult{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	session := &servermodels.ServiceSession{}
	err = db.NewSelect().Model(session).
		Join("JOIN channel_conversations AS cc ON cc.organization_id = ss.organization_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN conversation_participants AS cp ON cp.organization_id = cc.organization_id AND cp.conversation_id = cc.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
		Where("cc.organization_id = ?", channel.OrganizationID).
		Where("cc.conversation_id = ?", message.ConversationID).
		Where("cc.contact_channel_identity_id = ?", identity.ID).
		Where("ss.id = ?", *message.ServiceSessionID).
		Where("cp.id = ?", *message.SenderParticipantID).
		Where("cs.kind = ?", domain.ChatSubjectKindContact).
		Where("cs.source_id = ?", identity.ContactID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return InboundCustomerMessageResult{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	if err != nil {
		return InboundCustomerMessageResult{}, true, fmt.Errorf("check idempotent inbound customer message ownership: %w", err)
	}
	summary, err := loadConversationSummary(ctx, db, channel.OrganizationID, message.ConversationID, identity.ID)
	if err != nil {
		return InboundCustomerMessageResult{}, true, err
	}
	result := inboundCustomerMessageResult(summary, session, identity.ID, message, false)
	result.Attachment = attachment
	if storedReply != "" {
		result.ReplyTo, err = conversationaction.LoadMessageReference(ctx, db, channel.OrganizationID, message.ConversationID, storedReply)
		if err != nil {
			return InboundCustomerMessageResult{}, true, err
		}
	}
	return result, true, nil
}

// inboundCustomerMessageResult 构造渠道入站结果。
func inboundCustomerMessageResult(summary ConversationSummary, session *servermodels.ServiceSession, channelIdentityID string, message *servermodels.Message, inserted bool) InboundCustomerMessageResult {
	openedSession := session.OpeningMessageID == message.ID
	return InboundCustomerMessageResult{
		Summary: summary, Session: session, Message: message,
		ChannelIdentityID:    channelIdentityID,
		Inserted:             inserted,
		CreatedConversation:  openedSession && session.Sequence == 1,
		OpenedServiceSession: openedSession,
	}
}

// selectSingleChannelConversation 取得渠道身份固定映射的最早渠道会话。
func selectSingleChannelConversation(ctx context.Context, db bun.IDB, organizationID, channelIdentityID, requesterSubjectID, body, conversationID string) (*servermodels.Conversation, bool, error) {
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Join("JOIN channel_conversations AS cc ON cc.organization_id = cv.organization_id AND cc.conversation_id = cv.id").
		Where("cv.organization_id = ?", organizationID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cc.contact_channel_identity_id = ?", channelIdentityID).
		OrderExpr("cc.created_at ASC, cc.conversation_id ASC").
		Limit(1).
		Scan(ctx)
	if err == nil {
		return conversation, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("load channel identity customer conversation: %w", err)
	}
	return createChannelConversation(ctx, db, organizationID, channelIdentityID, requesterSubjectID, body, conversationID)
}

// lockInboundAttachmentFile 锁定该渠道访客上传的有效文件，按渠道入站上限校验字节数后激活。
func lockInboundAttachmentFile(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID string, attachment InboundCustomerAttachment) (*VisitorAttachment, error) {
	file := &servermodels.File{}
	err := db.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.organization_id = ?", attachment.FileID, channel.OrganizationID).
		Where("f.purpose = ? AND f.uploader_channel_identity_id = ?", domain.FilePurposeMessageAttachment, channelIdentityID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock inbound attachment file: %w", err)
	}
	if file.Status != string(domain.FileStatusUploaded) || file.Expired {
		return nil, fileaction.ErrFileNotFound
	}
	if limit := domain.ChannelInboundAttachmentLimit(domain.ChannelType(channel.Type)); limit > 0 && file.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	if _, err := db.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate inbound attachment file: %w", err)
	}
	return &VisitorAttachment{
		MessageAttachment: conversationaction.MessageAttachment{
			ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize,
			ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady,
		},
		StorageBackend: domain.FileStorageBackend(file.StorageBackend), StorageKey: file.StorageKey,
	}, nil
}

// createExternalMediaAttachment 为外部平台媒体建立取回中的文件记录，超过渠道入站上限的媒体直接落为取回失败。
func createExternalMediaAttachment(ctx context.Context, db bun.IDB, channel *servermodels.Channel, media InboundExternalMedia) (*VisitorAttachment, error) {
	attachment := &VisitorAttachment{MessageAttachment: conversationaction.MessageAttachment{
		Name: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
		ImageWidth: media.ImageWidth, ImageHeight: media.ImageHeight, TransferStatus: domain.MessageAttachmentTransferFailed,
	}}
	if limit := domain.ChannelInboundAttachmentLimit(domain.ChannelType(channel.Type)); media.ByteSize > limit {
		return attachment, nil
	}
	file, err := fileaction.CreateExternalAttachment(ctx, db, channel.OrganizationID, channel.CreatedByUserID, media.ExternalID, media.StorageBackend, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
	})
	if err != nil {
		return nil, err
	}
	attachment.ID, attachment.Name, attachment.ContentType = file.ID, file.OriginalName, file.ContentType
	attachment.TransferStatus = domain.MessageAttachmentTransferPending
	attachment.StorageBackend, attachment.StorageKey = domain.FileStorageBackend(file.StorageBackend), file.StorageKey
	return attachment, nil
}
