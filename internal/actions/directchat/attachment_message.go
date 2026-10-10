//go:build server

package directchat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/actions/membersend"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// SendAttachmentMessageAction 保存内部会话附件并激活文件。
type SendAttachmentMessageAction struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scheduler conversationaction.AgentChatMessageScheduler
}

// NewSendAttachmentMessageAction 创建附件消息发送操作。
func NewSendAttachmentMessageAction(db *bun.DB, enqueuer servertask.TxEnqueuer, scheduler conversationaction.AgentChatMessageScheduler) *SendAttachmentMessageAction {
	return &SendAttachmentMessageAction{db: db, enqueuer: enqueuer, scheduler: scheduler}
}

// Execute 在成员和会话锁内幂等发送一个已上传的附件，首发时按需创建单聊、AI 聊天或 Copilot 线程，AI 聊天与 Copilot 线程首次保存时追加 Agent 输入。
func (a *SendAttachmentMessageAction) Execute(ctx context.Context, identity *servermodels.Identity, input AttachmentMessageInput) (AttachmentMessageResult, error) {
	clientMessageID, valid := str.NormalizeUUID(input.ClientMessageID)
	input.ClientMessageID = clientMessageID
	input.Body = strings.TrimSpace(input.Body)
	fields := map[string]conversationaction.ValidationCode{}
	if !valid {
		fields["clientMessageId"] = conversationaction.ValidationClientMessageIDInvalid
	}
	if !str.IsUUID(input.FileID) || input.ImageWidth < 0 || input.ImageHeight < 0 {
		fields["fileId"] = conversationaction.ValidationFileIDInvalid
	}
	// 已有会话或 AI 聊天草稿编号与单聊目标恰好给出一个；AI 聊天与 Copilot 线程首发另外指定 AI 员工，不能同时给出单聊目标。
	if (input.ConversationID == "") == (input.TargetIdentityID == "") ||
		(input.ConversationID != "" && !str.IsUUID(input.ConversationID)) {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	if (input.TargetIdentityID != "" && !str.IsUUID(input.TargetIdentityID)) ||
		(input.AgentIdentityID != "" && input.TargetIdentityID != "") {
		fields["targetIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if input.AgentIdentityID != "" && !str.IsUUID(input.AgentIdentityID) {
		fields["agentIdentityId"] = conversationaction.ValidationTargetIdentityIDInvalid
	}
	if input.ServedConversationID != "" && (input.AgentIdentityID == "" || !str.IsUUID(input.ServedConversationID)) {
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
		serverstorage.UniqueDirectConversationPair: {},
	}, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		message := internalMessage{
			ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body,
			Attachment: &membersend.AttachmentIntent{FileID: input.FileID, ImageWidth: input.ImageWidth, ImageHeight: input.ImageHeight},
		}
		// 发送资格校验前先按发送编号读取本人保存的消息，向单聊目标首发时读取已有长期单聊中的消息。
		if input.TargetIdentityID != "" {
			existing, err := findDirectConversation(ctx, tx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, input.TargetIdentityID)
			if err != nil {
				return err
			}
			message.ConversationID = ""
			if existing != nil {
				message.ConversationID = existing.ID
			}
		}
		if message.ConversationID != "" {
			saved, found, err := replayInternalMessage(ctx, tx, identity, message)
			if err != nil {
				return err
			}
			if found {
				if input.AgentIdentityID != "" {
					if err := lockDraftOwnership(ctx, tx, identity, message.ConversationID, input.AgentIdentityID, input.ServedConversationID); err != nil {
						return err
					}
				}
				result, err = attachmentResult(ctx, tx, identity, input, message.ConversationID, saved)
				return err
			}
		}
		sendContext, err := lockAttachmentConversation(ctx, tx, identity, input)
		if err != nil {
			return err
		}
		message.ConversationID = sendContext.Conversation.ID
		saved, err := saveInternalMessage(ctx, tx, a.enqueuer, identity, message, sendContext, a.scheduler)
		if err != nil {
			return err
		}
		result, err = attachmentResult(ctx, tx, identity, input, message.ConversationID, saved)
		return err
	})
	if err != nil {
		return AttachmentMessageResult{}, err
	}
	return result, nil
}

// attachmentResult 组装附件发送结果，向单聊目标首发时附带单聊摘要，首发 AI 聊天时附带 AI 聊天摘要。
func attachmentResult(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, input AttachmentMessageInput, conversationID string, message conversationaction.ConversationMessage) (AttachmentMessageResult, error) {
	result := AttachmentMessageResult{ConversationID: conversationID, Message: message}
	if input.TargetIdentityID != "" {
		first, err := firstDirectResult(ctx, tx, identity, conversationID, input.TargetIdentityID, message)
		if err != nil {
			return AttachmentMessageResult{}, err
		}
		result.Conversation = &first.Conversation
	}
	if input.AgentIdentityID != "" && input.ServedConversationID == "" {
		summary, err := inboxaction.NewLoadInboxQuery(tx).LoadAgentConversation(ctx, identity, conversationID)
		if err != nil {
			return AttachmentMessageResult{}, err
		}
		result.AgentConversation = &summary
	}
	return result, nil
}

// lockAttachmentConversation 找到或创建附件所属的单聊、AI 聊天或 Copilot 线程并锁定发送资格，返回发送上下文。
func lockAttachmentConversation(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, input AttachmentMessageInput) (internalMessageContext, error) {
	conversationID := input.ConversationID
	if input.AgentIdentityID != "" {
		// AI 聊天草稿按草稿编号创建会话，标题取附件说明，没有说明时取文件名。
		title := input.Body
		if title == "" {
			err := tx.NewSelect().Model((*servermodels.File)(nil)).Column("original_name").
				Where("f.id = ? AND f.workspace_id = ? AND f.created_by_user_id = ? AND f.purpose = ?", input.FileID, identity.Workspace.ID, identity.User.ID, domain.FilePurposeMessageAttachment).Scan(ctx, &title)
			if errors.Is(err, sql.ErrNoRows) {
				return internalMessageContext{}, fileaction.ErrFileNotFound
			}
			if err != nil {
				return internalMessageContext{}, err
			}
		}
		// 指定所属客户会话时首发 Copilot 线程，否则首发 AI 聊天。
		if input.ServedConversationID != "" {
			if err := ensureServiceCopilotThread(ctx, tx, identity, conversationID, input.ServedConversationID, input.AgentIdentityID, title); err != nil {
				return internalMessageContext{}, err
			}
		} else if err := ensureAgentConversation(ctx, tx, identity, conversationID, input.AgentIdentityID, title); err != nil {
			return internalMessageContext{}, err
		}
	}
	if input.TargetIdentityID != "" {
		if input.TargetIdentityID == identity.WorkspaceIdentity.ID {
			return internalMessageContext{}, conversationaction.ErrDirectTargetNotFound
		}
		if _, err := loadDirectTarget(ctx, tx, identity.Workspace.ID, input.TargetIdentityID); err != nil {
			return internalMessageContext{}, err
		}
		conversation, err := findOrCreateDirectConversation(ctx, tx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, input.TargetIdentityID)
		if err != nil {
			return internalMessageContext{}, err
		}
		conversationID = conversation.ID
	}
	conversation, err := chatstate.LockConversation(ctx, tx, identity.Workspace.ID, conversationID)
	if err != nil {
		return internalMessageContext{}, err
	}
	// Copilot 线程按所属客户会话授权，提问成员在发送时加入线程参与者。
	if conversation.Type == string(domain.ConversationTypeCopilot) {
		return lockServiceCopilotSendContext(ctx, tx, identity, conversationID)
	}
	if conversation.Type == string(domain.ConversationTypeAgent) {
		return lockAgentSendContext(ctx, tx, identity, conversationID)
	}
	member, err := chatstate.LockMember(ctx, tx, identity, conversationID)
	if err != nil {
		return internalMessageContext{}, err
	}
	switch domain.ConversationType(member.Conversation.Type) {
	case domain.ConversationTypeDirect:
		if input.TargetIdentityID != "" {
			if err := reactivateDirectConversation(ctx, tx, member.Conversation); err != nil {
				return internalMessageContext{}, err
			}
		}
		sendContext, err := loadDirectSendContext(ctx, tx, identity, conversationID)
		sendContext.Conversation = member.Conversation
		return sendContext, err
	case domain.ConversationTypeGroup:
		if member.Conversation.Status != string(domain.ConversationStatusActive) {
			return internalMessageContext{}, conversationaction.ErrConversationNotFound
		}
		return internalMessageContext{Conversation: member.Conversation, ConversationID: conversationID, ParticipantID: member.ParticipantID, SubjectID: member.SubjectID}, nil
	default:
		return internalMessageContext{}, conversationaction.ErrConversationNotFound
	}
}
