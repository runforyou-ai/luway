//go:build server

package directchat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/uptrace/bun"
)

// SendAttachmentMessageAction 保存内部会话附件并激活文件。
type SendAttachmentMessageAction struct {
	db        *bun.DB
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendAttachmentMessageAction 创建附件消息发送操作。
func NewSendAttachmentMessageAction(db *bun.DB, scheduler conversationaction.AgentChatMessageScheduler) *SendAttachmentMessageAction {
	return &SendAttachmentMessageAction{db: db, scheduler: scheduler}
}

// Execute 在成员和会话锁内幂等发送一个已上传的附件，首发时按需创建单聊、AI 聊天或 Copilot 线程，AI 聊天与 Copilot 线程首次保存时追加 Agent 输入。
func (a *SendAttachmentMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input AttachmentMessageInput) (AttachmentMessageResult, error) {
	clientMessageID, valid := common.NormalizeUUID(input.ClientMessageID)
	input.ClientMessageID = clientMessageID
	input.Body = strings.TrimSpace(input.Body)
	fields := map[string]conversationaction.ValidationCode{}
	if !valid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	if !common.ValidUUID(input.FileID) || input.ImageWidth < 0 || input.ImageHeight < 0 {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	// 已有会话或 AI 聊天草稿编号与单聊目标恰好给出一个；AI 聊天与 Copilot 线程首发另外指定 AI 员工，不能同时给出单聊目标。
	if (input.ConversationID == "") == (input.TargetIdentityID == "") ||
		(input.ConversationID != "" && !common.ValidUUID(input.ConversationID)) {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	if (input.TargetIdentityID != "" && !common.ValidUUID(input.TargetIdentityID)) ||
		(input.AgentIdentityID != "" && input.TargetIdentityID != "") {
		fields["targetIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if input.AgentIdentityID != "" && !common.ValidUUID(input.AgentIdentityID) {
		fields["agentIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if input.ServedConversationID != "" && (input.AgentIdentityID == "" || !common.ValidUUID(input.ServedConversationID)) {
		fields["servedConversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	if utf8.RuneCountInString(input.Body) > conversationaction.MaxMessageBodyRunes {
		fields["body"] = conversationaction.ValidationBodyTooLong
	}
	if len(fields) > 0 {
		return AttachmentMessageResult{}, &conversationaction.ValidationError{Fields: fields}
	}
	var result AttachmentMessageResult
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, map[string]struct{}{
		"direct_conversations_organization_identity_pair_unique": {},
	}, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		member, agentContext, err := lockAttachmentConversation(ctx, tx, identity, input)
		if err != nil {
			return err
		}
		message, session, inserted, err := saveAttachmentMessage(ctx, tx, identity, member, agentContext, input)
		if err != nil {
			return err
		}
		if agentContext != nil && inserted {
			if err := scheduleAgentChatInput(ctx, tx, identity.Organization.ID, *agentContext, session, message.ID, a.scheduler); err != nil {
				return err
			}
		}
		result = AttachmentMessageResult{ConversationID: member.Conversation.ID, Message: message}
		if input.TargetIdentityID != "" {
			target, err := loadDirectTarget(ctx, tx, identity.Organization.ID, input.TargetIdentityID)
			if err != nil {
				return err
			}
			summary, err := loadDirectConversationSummary(ctx, tx, identity.Organization.ID, member.Conversation.ID, target)
			if err != nil {
				return err
			}
			result.Conversation = &summary
		}
		if input.AgentIdentityID != "" && input.ServedConversationID == "" {
			summary, err := inboxaction.NewLoadInboxQuery(tx).LoadAgentConversation(ctx, identity, member.Conversation.ID)
			if err != nil {
				return err
			}
			result.AgentConversation = &summary
		}
		return nil
	})
	if err != nil {
		return AttachmentMessageResult{}, err
	}
	return result, nil
}

// lockAttachmentConversation 找到或创建附件所属的单聊、AI 聊天或 Copilot 线程并锁定发送资格，AI 聊天与 Copilot 线程同时返回 Agent 发送上下文。
func lockAttachmentConversation(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, input AttachmentMessageInput) (chatstate.Member, *internalMessageContext, error) {
	conversationID := input.ConversationID
	if input.AgentIdentityID != "" {
		// AI 聊天草稿按草稿编号创建会话，标题取附件说明，没有说明时取文件名。
		title := input.Body
		if title == "" {
			err := tx.NewSelect().Model((*servermodels.File)(nil)).Column("original_name").
				Where("f.id = ? AND f.organization_id = ? AND f.created_by_user_id = ? AND f.purpose = ?", input.FileID, identity.Organization.ID, identity.User.ID, domain.FilePurposeMessageAttachment).Scan(ctx, &title)
			if errors.Is(err, sql.ErrNoRows) {
				return chatstate.Member{}, nil, fileaction.ErrFileNotFound
			}
			if err != nil {
				return chatstate.Member{}, nil, err
			}
		}
		// 指定所属客户会话时首发 Copilot 线程，否则首发 AI 聊天。
		if input.ServedConversationID != "" {
			if err := ensureServiceCopilotThread(ctx, tx, identity, conversationID, input.ServedConversationID, input.AgentIdentityID, title); err != nil {
				return chatstate.Member{}, nil, err
			}
		} else if err := ensureAgentConversation(ctx, tx, identity, conversationID, input.AgentIdentityID, title); err != nil {
			return chatstate.Member{}, nil, err
		}
	}
	if input.TargetIdentityID != "" {
		if input.TargetIdentityID == identity.OrganizationIdentity.ID {
			return chatstate.Member{}, nil, conversationaction.ErrDirectTargetNotFound
		}
		if _, err := loadDirectTarget(ctx, tx, identity.Organization.ID, input.TargetIdentityID); err != nil {
			return chatstate.Member{}, nil, err
		}
		conversation, err := findOrCreateDirectConversation(ctx, tx, identity.Organization.ID, identity.OrganizationIdentity.ID, input.TargetIdentityID)
		if err != nil {
			return chatstate.Member{}, nil, err
		}
		conversationID = conversation.ID
	}
	conversation, err := chatstate.LockConversation(ctx, tx, identity.Organization.ID, conversationID)
	if err != nil {
		return chatstate.Member{}, nil, err
	}
	// Copilot 线程按所属客户会话授权，提问成员在发送时加入线程参与者。
	if conversation.Type == string(domain.ConversationTypeCopilot) {
		copilotContext, err := lockServiceCopilotSendContext(ctx, tx, identity, conversationID)
		if err != nil {
			return chatstate.Member{}, nil, err
		}
		return chatstate.Member{Conversation: copilotContext.Conversation, ParticipantID: copilotContext.ParticipantID, SubjectID: copilotContext.SubjectID}, &copilotContext, nil
	}
	member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
	if err != nil {
		return member, nil, err
	}
	switch domain.ConversationType(member.Conversation.Type) {
	case domain.ConversationTypeAgent:
		agentContext, err := lockAgentSendContext(ctx, tx, identity, conversationID)
		return member, &agentContext, err
	case domain.ConversationTypeDirect:
		if input.TargetIdentityID != "" {
			if err := reactivateDirectConversation(ctx, tx, member.Conversation); err != nil {
				return member, nil, err
			}
		}
		_, err = loadDirectSendContext(ctx, tx, identity, conversationID)
		return member, nil, err
	case domain.ConversationTypeGroup:
		if member.Conversation.Status != string(domain.ConversationStatusActive) {
			return member, nil, conversationaction.ErrConversationNotFound
		}
		return member, nil, nil
	default:
		return member, nil, conversationaction.ErrConversationNotFound
	}
}

// saveAttachmentMessage 校验完整发送意图并在同一事务内保存消息、附件、文件激活和阅读位置，AI 聊天中的消息按 AI 员工的服务对象进入服务周期；返回所属服务周期与是否新建消息。
func saveAttachmentMessage(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, member chatstate.Member, agentContext *internalMessageContext, input AttachmentMessageInput) (conversationaction.ConversationMessage, *servermodels.ServiceSession, bool, error) {
	key := "mmsg:" + identity.OrganizationIdentity.ID + ":" + input.ClientMessageID
	expectation := conversationaction.InternalAttachmentExpectation(member.Conversation.ID, input.Body, conversationaction.AttachmentExpectation{
		FileID: input.FileID, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight,
	})
	if saved, found, err := conversationaction.LoadIdempotentMemberMessage(ctx, tx, identity, expectation, key); err != nil || found {
		return saved, nil, false, err
	}
	file := &servermodels.File{}
	err := tx.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Where("f.id = ? AND f.organization_id = ? AND f.created_by_user_id = ?", input.FileID, identity.Organization.ID, identity.User.ID).
		Where("f.purpose = ?", domain.FilePurposeMessageAttachment).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationaction.ConversationMessage{}, nil, false, fileaction.ErrFileNotFound
	}
	if err != nil {
		return conversationaction.ConversationMessage{}, nil, false, err
	}
	if file.Status != string(domain.FileStatusUploaded) || file.Expired {
		return conversationaction.ConversationMessage{}, nil, false, fileaction.ErrFileNotFound
	}
	message := &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, ConversationID: member.Conversation.ID,
		SenderParticipantID: &member.ParticipantID, Type: string(domain.MessageTypeAttachment), Body: input.Body,
		SearchVector:    searchtext.Vector(input.Body, file.OriginalName),
		ClientMessageID: &input.ClientMessageID, IdempotencyKey: &key, OriginatedAt: time.Now().UTC(),
	}
	var session *servermodels.ServiceSession
	if agentContext != nil && agentContext.AgentInputKind == domain.AgentInputKindAgentDirect {
		if session, err = directServiceSession(ctx, tx, identity.Organization.ID, *agentContext, message.ID, message.OriginatedAt); err != nil {
			return conversationaction.ConversationMessage{}, nil, false, err
		}
		if session != nil {
			message.ServiceSessionID = &session.ID
		}
	}
	message, _, err = chatstate.AppendMessage(ctx, tx, member.Conversation, message)
	if err != nil {
		return conversationaction.ConversationMessage{}, nil, false, err
	}
	if _, err := tx.NewRaw(`INSERT INTO message_attachments
 (message_id, organization_id, file_id, name, content_type, byte_size, image_width, image_height, transfer_status)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID, identity.Organization.ID, file.ID, file.OriginalName, file.ContentType, file.ByteSize, input.ImageWidth, input.ImageHeight, domain.MessageAttachmentTransferReady).Exec(ctx); err != nil {
		return conversationaction.ConversationMessage{}, nil, false, err
	}
	if _, err := tx.NewUpdate().Model(file).Set("status = ?", domain.FileStatusActive).Set("expires_at = NULL").Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
		return conversationaction.ConversationMessage{}, nil, false, err
	}
	// Copilot 线程不维护个人会话状态，其余会话推进本人阅读水位。
	if member.Conversation.Type != string(domain.ConversationTypeCopilot) {
		if err := conversationaction.AdvanceConversationUserReadState(ctx, tx, &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: member.Conversation.ID, UserID: identity.User.ID, LastReadMessageID: &message.ID,
		}, message); err != nil {
			return conversationaction.ConversationMessage{}, nil, false, err
		}
	}
	result := conversationaction.MemberConversationMessage(message, member.SubjectID, identity.OrganizationIdentity)
	result.Attachment = &conversationaction.MessageAttachment{ID: file.ID, Name: file.OriginalName, ContentType: file.ContentType, ByteSize: file.ByteSize, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight, TransferStatus: domain.MessageAttachmentTransferReady}
	return result, session, true, nil
}
