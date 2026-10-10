//go:build server

package channelinbound

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
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Input 定义渠道文本与附件入站事务的稳定事实。
type Input struct {
	ChannelMessage   *channelmessage.Inbound
	ClientMessageID  *string
	Attachment       *UploadedFile
	ExternalMedia    *ExternalMedia
	ReplyToMessageID string
	ExternalID       string
	// Unsupported 表示发起人发送的消息内容类型暂不支持，写入一条正文为空的不支持消息。
	Unsupported bool
	// IdentityAsserted 表示本次入站请求自身给出核验结论（网站与业务系统转发），新消息按 VerifiedUserID 同步核验；为假时渠道身份的核验由渠道身份断言维护，入站保持不变。
	IdentityAsserted        bool
	VerifiedUserID          string
	Email                   string
	SignedProfile           *domain.SignedContactProfile
	VisitorContext          *domain.VisitorContext
	DisplayName             *string
	RequestedConversationID *string
	SingleConversation      bool
	Body                    string
	IdempotencyKey          string
	OriginatedAt            time.Time
}

// UploadedFile 定义网站访客附件消息待关联的已上传文件。
type UploadedFile struct {
	FileID      string
	ImageWidth  int
	ImageHeight int
}

// ExternalMedia 定义外部平台随消息送达、内容尚待取回的媒体附件。
type ExternalMedia struct {
	ExternalID     string
	FileName       string
	ContentType    string
	ByteSize       int64
	ImageWidth     int
	ImageHeight    int
	StorageBackend domain.FileStorageBackend
}

// messageType 返回本次入站写入的消息类型。
func (i Input) messageType() domain.MessageType {
	if i.Unsupported {
		return domain.MessageTypeUnsupported
	}
	if i.Attachment != nil || i.ExternalMedia != nil {
		return domain.MessageTypeAttachment
	}
	return domain.MessageTypeText
}

// Result 返回渠道入站事务创建或取得的事实。
type Result struct {
	ReplyTo              *conversationaction.ConversationMessageReference
	Attachment           *Attachment
	Session              *servermodels.ServiceSession
	Message              *servermodels.Message
	ChannelIdentityID    string
	Inserted             bool
	CreatedConversation  bool
	OpenedServiceSession bool
}

// errNewSessionRouteRequired 表示本次写入需要开启新客服处理周期，须先锁定路由目标再重做。
var errNewSessionRouteRequired = errors.New("new service session route required")

// Receive 在调用方事务中幂等写入渠道发起人的文本或附件消息：服务客户的渠道按入站事实建立或同步联系人，服务员工的渠道要求渠道身份已绑定成员，未绑定时返回 ErrIdentityUnbound；新客服处理周期路由到队列时投递分配任务，路由目标先于渠道身份与会话取共享锁。
func Receive(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, channel *servermodels.Channel, input Input) (Result, error) {
	ids := generateIDs()
	// 服务员工的渠道先不加锁读取绑定成员，新周期的路由不交给发起成员本人。
	requesterIdentityID := ""
	if domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Audience == domain.ServiceAudienceEmployee {
		if err := db.NewSelect().Model((*servermodels.ChannelIdentity)(nil)).ColumnExpr("COALESCE(user_identity_id::text, '')").
			Where("workspace_id = ? AND channel_id = ? AND external_id = ?", channel.WorkspaceID, channel.ID, input.ExternalID).
			Scan(ctx, &requesterIdentityID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Result{}, fmt.Errorf("load channel requester member: %w", err)
		}
	}
	// 不加锁预判目标会话是否已有进行中周期：单会话写入取该渠道身份最近有消息的会话，指定会话时取访客指定的会话，其余情况新建会话。
	open := false
	if input.SingleConversation || input.RequestedConversationID != nil {
		query := db.NewSelect().TableExpr("channel_identities AS ci").
			Join("JOIN channel_conversations AS cc ON cc.workspace_id = ci.workspace_id AND cc.channel_identity_id = ci.id").
			Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
			Join("JOIN service_sessions AS ss ON ss.workspace_id = svc.workspace_id AND ss.id = svc.current_service_session_id").
			Where("ci.workspace_id = ? AND ci.channel_id = ? AND ci.external_id = ?", channel.WorkspaceID, channel.ID, input.ExternalID).
			Where("ss.status = ?", domain.ServiceSessionStatusOpen)
		if input.RequestedConversationID != nil {
			query = query.Where("cc.conversation_id = ?", *input.RequestedConversationID)
		} else {
			query = query.Where(`cc.conversation_id = (SELECT recent.conversation_id FROM channel_conversations AS recent
				JOIN conversations AS recent_cv ON recent_cv.workspace_id = recent.workspace_id AND recent_cv.id = recent.conversation_id
				WHERE recent.workspace_id = ci.workspace_id AND recent.channel_identity_id = ci.id
				ORDER BY recent_cv.last_message_at DESC NULLS LAST, recent.conversation_id DESC LIMIT 1)`)
		}
		var err error
		if open, err = query.Exists(ctx); err != nil {
			return Result{}, fmt.Errorf("check open service session: %w", err)
		}
	}
	if !open {
		route, err := serviceroute.ResolveNewSessionRoute(ctx, db, channel, requesterIdentityID)
		if err != nil {
			return Result{}, err
		}
		return receive(ctx, db, enqueuer, channel, input, ids, &route)
	}
	if _, err := db.ExecContext(ctx, "SAVEPOINT inbound_channel_message"); err != nil {
		return Result{}, fmt.Errorf("create inbound message savepoint: %w", err)
	}
	result, err := receive(ctx, db, enqueuer, channel, input, ids, nil)
	if !errors.Is(err, errNewSessionRouteRequired) {
		return result, err
	}
	// 预判后周期已关闭时回滚到保存点，锁定路由目标后重做写入。
	if _, err := db.ExecContext(ctx, "ROLLBACK TO SAVEPOINT inbound_channel_message"); err != nil {
		return Result{}, fmt.Errorf("rollback inbound message savepoint: %w", err)
	}
	route, err := serviceroute.ResolveNewSessionRoute(ctx, db, channel, requesterIdentityID)
	if err != nil {
		return Result{}, err
	}
	return receive(ctx, db, enqueuer, channel, input, ids, &route)
}

// receive 执行一次入站写入；route 为空且需要开启新周期时返回 errNewSessionRouteRequired。
func receive(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, channel *servermodels.Channel, input Input, ids generatedIDs, route *serviceroute.RouteSnapshot) (Result, error) {
	employee := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Audience == domain.ServiceAudienceEmployee
	identityInput := contactaction.EnsureChannelIdentityInput{
		WorkspaceID: channel.WorkspaceID,
		ChannelID:   channel.ID,
		ExternalID:  input.ExternalID,
		ContactID:   ids.contact,
		IdentityID:  ids.channelIdentity,
		Email:       input.Email,
	}
	var ensured contactaction.EnsuredChannelIdentity
	var identity *servermodels.ChannelIdentity
	if employee {
		var err error
		if identity, err = LoadBoundIdentity(ctx, db, channel, input.ExternalID); err != nil {
			return Result{}, err
		}
	} else {
		// 入站自带核验结论时按签名身份建立渠道身份，否则新建的渠道身份为未核验。
		if input.IdentityAsserted {
			identityInput.VerifiedUserID = input.VerifiedUserID
		}
		var err error
		if ensured, err = contactaction.EnsureChannelIdentity(ctx, db, identityInput); err != nil {
			return Result{}, err
		}
		identity = ensured.Identity
	}
	// 网站发送编号按渠道访客身份隔离，外部平台保留来源幂等键。
	if input.ClientMessageID != nil {
		input.IdempotencyKey = "chmsg:" + identity.ID + ":" + *input.ClientMessageID
	}
	// 渠道身份名称变化时同时推进其所在渠道会话的版本，重放与幂等冲突同样覆盖。
	if input.DisplayName != nil && (identity.DisplayName == nil || *identity.DisplayName != *input.DisplayName) {
		if _, err := db.NewUpdate().Model(identity).
			Set("display_name = ?", *input.DisplayName).
			WherePK().
			Where("workspace_id = ?", channel.WorkspaceID).
			Exec(ctx); err != nil {
			return Result{}, fmt.Errorf("update channel identity display name: %w", err)
		}
		identity.DisplayName = input.DisplayName
		if err := chatstate.TouchChannelIdentityConversations(ctx, db, channel.WorkspaceID, identity.ID); err != nil {
			return Result{}, err
		}
	}
	if saved, found, err := loadReceived(ctx, db, channel, identity, input); err != nil || found {
		return saved, err
	}
	// 服务员工的渠道以绑定成员为发起人；服务客户的渠道按本次签名身份同步渠道身份的核验状态、所属联系人与邮箱并以联系人为发起人，重放的消息不改变身份，入站不给出核验结论时保持现有核验。
	var subject *servermodels.ChatSubject
	var err error
	if employee {
		if subject, err = chatstate.EnsureSubject(ctx, db, channel.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, *identity.UserIdentityID, ids.subject); err != nil {
			return Result{}, err
		}
	} else {
		if !input.IdentityAsserted && identity.VerifiedUserID != nil {
			identityInput.VerifiedUserID = *identity.VerifiedUserID
		}
		if ensured, err = contactaction.SyncChannelIdentity(ctx, db, enqueuer, identityInput, ensured); err != nil {
			return Result{}, err
		}
		// 签名身份带入的档案只随新写入的消息更新。
		if input.SignedProfile != nil {
			if err := contactprofileaction.ApplySignedProfile(ctx, db, channel.WorkspaceID, ensured.Contact.ID, *input.SignedProfile); err != nil {
				return Result{}, err
			}
		}
		if subject, err = chatstate.EnsureSubject(ctx, db, channel.WorkspaceID, domain.ChatSubjectKindContact, ensured.Contact.ID, ids.subject); err != nil {
			return Result{}, err
		}
	}

	// 访客上传的附件在会话之前锁定并激活，外部媒体先建立取回中的文件记录；文件名用于新会话标题和检索向量。
	var attachment *Attachment
	titleSource := input.Body
	if input.Attachment != nil {
		attachment, err = lockInboundAttachmentFile(ctx, db, channel, identity.ID, *input.Attachment)
		if err != nil {
			return Result{}, err
		}
	} else if input.ExternalMedia != nil {
		attachment, err = createExternalMediaAttachment(ctx, db, channel, *input.ExternalMedia)
		if err != nil {
			return Result{}, err
		}
	}
	if attachment != nil && titleSource == "" {
		titleSource = attachment.Name
	}

	var conversation *servermodels.Conversation
	var insertedConversation bool
	if input.SingleConversation {
		conversation, insertedConversation, err = selectSingleConversation(ctx, db, channel, identity.ID, subject.ID, titleSource, ids.conversation)
	} else {
		conversation, insertedConversation, err = selectTargetConversation(ctx, db, channel, identity.ID, subject.ID, input.RequestedConversationID, titleSource, ids.conversation)
	}
	if err != nil {
		return Result{}, err
	}
	// 渠道身份稳定后锁定会话，已有线程随后锁当前周期并核对渠道身份。
	conversation, err = chatstate.LockChannelConversation(ctx, db, channel.WorkspaceID, conversation.ID)
	if err != nil {
		return Result{}, err
	}
	replyTo, err := conversationaction.LoadConversationReplyTarget(ctx, db, channel.WorkspaceID, conversation.ID, input.ReplyToMessageID)
	if err != nil {
		return Result{}, err
	}
	// 发起人只能引用对其可见的消息。
	if replyTo != nil && replyTo.Visibility == domain.MessageVisibilityInternal {
		return Result{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
	}

	// 新会话尚无周期；已有会话锁定服务会话并取进行中的当前周期。
	var session *servermodels.ServiceSession
	if !insertedConversation {
		session, err = chatstate.LockOpenServiceSession(ctx, db, channel.WorkspaceID, conversation.ID)
		if err != nil {
			return Result{}, err
		}
	}
	// 网站消息在确定写入周期后取数据库时刻，已有会话此时已持有周期锁；外部渠道保留来源时间。
	if input.OriginatedAt.IsZero() {
		if input.OriginatedAt, err = serverstorage.ClockNow(ctx, db); err != nil {
			return Result{}, err
		}
	}
	if conversation.Status == string(domain.ConversationStatusArchived) {
		if _, err := db.NewUpdate().Model(conversation).
			Set("status = ?", domain.ConversationStatusActive).
			WherePK().
			Where("workspace_id = ?", channel.WorkspaceID).
			Exec(ctx); err != nil {
			return Result{}, fmt.Errorf("reactivate channel conversation: %w", err)
		}
		conversation.Status = string(domain.ConversationStatusActive)
	}

	// 追加消息之外的会话变化类别：新周期改变服务周期与访客资料，访客上下文变化改变访客资料。
	var changes domain.ConversationChanges
	if session == nil {
		if route == nil {
			return Result{}, errNewSessionRouteRequired
		}
		changes = domain.ConversationChangeService | domain.ConversationChangeParticipants
		// 路由到 AI 员工时记为其接待。
		var agentIdentityID *string
		if route.AssigneeType == domain.WorkspaceIdentityTypeAgent {
			agentIdentityID = route.AssigneeIdentityID
		}
		session, err = chatstate.OpenServiceSession(ctx, db, channel.WorkspaceID, conversation.ID, chatstate.OpenServiceSessionInput{
			ID: ids.serviceSession, OpeningMessageID: ids.message, OpenedAt: input.OriginatedAt,
			TeamID: route.TeamID, AssigneeIdentityID: route.AssigneeIdentityID, AgentIdentityID: agentIdentityID, VisitorContext: input.VisitorContext,
		})
		if err != nil {
			return Result{}, err
		}
		if route.AssigneeIdentityID == nil {
			if err := serviceassignment.EnqueueAssign(ctx, db, enqueuer, serviceassignment.AssignInput{
				WorkspaceID: channel.WorkspaceID, ServiceSessionID: session.ID,
			}); err != nil {
				return Result{}, err
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
			if err := servicestate.SaveVisitorContext(ctx, db, session); err != nil {
				return Result{}, err
			}
			changes = domain.ConversationChangeParticipants
		}
	}

	participant, err := chatstate.EnsureParticipant(ctx, db, channel.WorkspaceID, conversation.ID, subject.ID, ids.participant)
	if err != nil {
		return Result{}, err
	}

	message := &servermodels.Message{
		ID: ids.message, WorkspaceID: channel.WorkspaceID,
		ConversationID: conversation.ID, ServiceSessionID: &session.ID,
		SenderParticipantID: &participant.ID, Type: string(input.messageType()),
		ClientMessageID: input.ClientMessageID, Body: input.Body, IdempotencyKey: &input.IdempotencyKey,
		OriginatedAt: input.OriginatedAt,
	}
	if replyTo != nil {
		message.ReplyToMessageID = &replyTo.ID
	}
	if attachment != nil {
		message.SearchVector = searchtext.Vector(input.Body, attachment.Name)
	}
	message, inserted, err := chatstate.AppendServiceMessage(ctx, db, enqueuer, conversation, session, message)
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		saved, _, err := loadReceived(ctx, db, channel, identity, input)
		return saved, err
	}
	if changes != 0 {
		if err := chatstate.NotifyConversationChanged(ctx, db, conversation, changes); err != nil {
			return Result{}, err
		}
	}
	if attachment != nil {
		if err := conversationaction.SaveMessageAttachment(ctx, db, channel.WorkspaceID, message.ID, attachment.MessageAttachment); err != nil {
			return Result{}, err
		}
	}
	if input.ChannelMessage != nil {
		if err := channelmessage.RecordInbound(ctx, db, channel.ID, message, input.ChannelMessage); err != nil {
			return Result{}, err
		}
	}
	if _, err := db.NewUpdate().Model(identity).
		Set("last_seen_at = CASE WHEN last_seen_at IS NULL OR last_seen_at < ? THEN ? ELSE last_seen_at END", input.OriginatedAt, input.OriginatedAt).
		WherePK().
		Where("workspace_id = ?", channel.WorkspaceID).
		Exec(ctx); err != nil {
		return Result{}, fmt.Errorf("update channel identity last seen: %w", err)
	}

	result := newResult(session, identity.ID, message, true)
	result.ReplyTo = replyTo
	result.Attachment = attachment
	return result, nil
}

// loadReceived 校验并返回已经写入的渠道消息。
func loadReceived(ctx context.Context, db bun.IDB, channel *servermodels.Channel, identity *servermodels.ChannelIdentity, input Input) (Result, bool, error) {
	message := &servermodels.Message{}
	err := db.NewSelect().Model(message).
		Where("msg.workspace_id = ?", channel.WorkspaceID).
		Where("msg.idempotency_key = ?", input.IdempotencyKey).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("find idempotent inbound channel message: %w", err)
	}
	if message.ServiceSessionID == nil || message.SenderParticipantID == nil || message.DeletedAt != nil {
		return Result{}, true, conversationaction.ErrDataInvariant
	}
	if message.Type != string(input.messageType()) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	var attachment *Attachment
	if input.Attachment != nil || input.ExternalMedia != nil {
		attachment = &Attachment{}
		if err := db.NewSelect().TableExpr("message_attachments AS ma").
			ColumnExpr("COALESCE(ma.file_id::text, '') AS id").
			ColumnExpr("ma.name, ma.content_type, ma.byte_size, ma.image_width, ma.image_height, ma.transfer_status").
			ColumnExpr("f.storage_backend, f.storage_key").
			Join("LEFT JOIN files AS f ON f.id = ma.file_id AND f.workspace_id = ma.workspace_id").
			Where("ma.message_id = ? AND ma.workspace_id = ?", message.ID, channel.WorkspaceID).
			Scan(ctx, attachment); err != nil {
			return Result{}, true, fmt.Errorf("load idempotent inbound attachment: %w", err)
		}
		// 访客附件核对文件编号；外部媒体的文件编号、字节数、文件名与类型随取回结果变化，只核对图片尺寸。
		if input.Attachment != nil && (attachment.ID != input.Attachment.FileID || attachment.ImageWidth != input.Attachment.ImageWidth || attachment.ImageHeight != input.Attachment.ImageHeight) {
			return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
		if media := input.ExternalMedia; media != nil && (attachment.ImageWidth != media.ImageWidth || attachment.ImageHeight != media.ImageHeight) {
			return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
	}
	storedReply := support.Deref(message.ReplyToMessageID)
	replyMatches := storedReply == input.ReplyToMessageID
	if input.ChannelMessage != nil {
		replyMatches, err = channelmessage.MatchesInbound(ctx, db, message, channel.ID, input.ChannelMessage)
		if err != nil {
			return Result{}, true, err
		}
	}
	if !replyMatches || message.Body != input.Body || (input.RequestedConversationID != nil && *input.RequestedConversationID != message.ConversationID) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	session := &servermodels.ServiceSession{}
	err = db.NewSelect().Model(session).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = ss.workspace_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN conversation_participants AS cp ON cp.workspace_id = cc.workspace_id AND cp.conversation_id = cc.conversation_id AND cp.subject_id = svc.requester_subject_id").
		Where("cc.workspace_id = ?", channel.WorkspaceID).
		Where("cc.conversation_id = ?", message.ConversationID).
		Where("cc.channel_identity_id = ?", identity.ID).
		Where("ss.id = ?", *message.ServiceSessionID).
		Where("cp.id = ?", *message.SenderParticipantID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	if err != nil {
		return Result{}, true, fmt.Errorf("check idempotent inbound channel message ownership: %w", err)
	}
	result := newResult(session, identity.ID, message, false)
	result.Attachment = attachment
	if storedReply != "" {
		result.ReplyTo, err = conversationaction.LoadMessageReference(ctx, db, channel.WorkspaceID, message.ConversationID, storedReply)
		if err != nil {
			return Result{}, true, err
		}
	}
	return result, true, nil
}

// newResult 构造渠道入站结果。
func newResult(session *servermodels.ServiceSession, channelIdentityID string, message *servermodels.Message, inserted bool) Result {
	openedSession := session.OpeningMessageID == message.ID
	return Result{
		Session: session, Message: message,
		ChannelIdentityID:    channelIdentityID,
		Inserted:             inserted,
		CreatedConversation:  openedSession && session.Sequence == 1,
		OpenedServiceSession: openedSession,
	}
}

// lockInboundAttachmentFile 锁定该渠道访客上传的有效文件，按渠道入站上限校验字节数后激活。
func lockInboundAttachmentFile(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID string, attachment UploadedFile) (*Attachment, error) {
	file := &servermodels.File{}
	err := db.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.workspace_id = ?", attachment.FileID, channel.WorkspaceID).
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
	if limit := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).InboundAttachmentLimit; file.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	if _, err := db.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate inbound attachment file: %w", err)
	}
	return &Attachment{
		MessageAttachment: conversationaction.MessageAttachment{
			ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize,
			ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady,
		},
		StorageBackend: domain.FileStorageBackend(file.StorageBackend), StorageKey: file.StorageKey,
	}, nil
}

// createExternalMediaAttachment 为外部平台媒体建立取回中的文件记录，超过渠道入站上限的媒体直接落为取回失败。
func createExternalMediaAttachment(ctx context.Context, db bun.IDB, channel *servermodels.Channel, media ExternalMedia) (*Attachment, error) {
	attachment := &Attachment{MessageAttachment: conversationaction.MessageAttachment{
		Name: media.FileName, ContentType: media.ContentType, ByteSize: media.ByteSize,
		ImageWidth: media.ImageWidth, ImageHeight: media.ImageHeight, TransferStatus: domain.MessageAttachmentTransferFailed,
	}}
	if limit := domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).InboundAttachmentLimit; media.ByteSize > limit {
		return attachment, nil
	}
	file, err := fileaction.CreateExternalAttachment(ctx, db, channel.WorkspaceID, channel.CreatedByUserID, media.ExternalID, media.StorageBackend, fileaction.UploadInput{
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
