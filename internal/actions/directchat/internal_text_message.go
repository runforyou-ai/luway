//go:build server

package directchat

import (
	"context"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// saveInternalTextMessage 在会话与成员已锁定的事务内幂等保存消息、个人状态和 Agent 输入，AI 聊天中的消息按 AI 员工的服务对象进入服务周期。
func saveInternalTextMessage(ctx context.Context, db bun.IDB, identity *servermodels.Identity, input InternalTextMessageInput, sendContext internalMessageContext, agentScheduler conversationaction.AgentChatMessageScheduler) (conversationaction.ConversationMessage, error) {
	idempotencyKey := "mmsg:" + identity.OrganizationIdentity.ID + ":" + input.ClientMessageID
	if saved, found, err := conversationaction.LoadIdempotentMemberMessage(ctx, db, identity, conversationaction.InternalTextExpectation(input.ConversationID, input.Body, input.ReplyToMessageID), idempotencyKey); err != nil || found {
		return saved, err
	}
	replyTo, err := conversationaction.LoadConversationReplyTarget(ctx, db, identity.Organization.ID, input.ConversationID, input.ReplyToMessageID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	message := &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID,
		ConversationID: input.ConversationID, SenderParticipantID: &sendContext.ParticipantID,
		Type: string(domain.MessageTypeText), Body: input.Body,
		ClientMessageID: &input.ClientMessageID, IdempotencyKey: &idempotencyKey, OriginatedAt: time.Now().UTC(),
	}
	if replyTo != nil {
		// 只能引用会话各方可见的消息。
		if replyTo.Visibility != domain.MessageVisibilityShared {
			return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
		}
		message.ReplyToMessageID = &replyTo.ID
	}
	// AI 聊天中的发起人消息按 AI 员工的服务对象进入服务周期。
	var session *servermodels.ServiceSession
	if sendContext.AgentInputKind == domain.AgentInputKindAgentDirect {
		if session, err = directServiceSession(ctx, db, identity.Organization.ID, sendContext, message.ID, message.OriginatedAt); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
		if session != nil {
			message.ServiceSessionID = &session.ID
		}
	}
	message, inserted, err := chatstate.AppendMessage(ctx, db, sendContext.Conversation, message)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if !inserted {
		saved, _, err := conversationaction.LoadIdempotentMemberMessage(ctx, db, identity, conversationaction.InternalTextExpectation(input.ConversationID, input.Body, input.ReplyToMessageID), idempotencyKey)
		return saved, err
	}
	// Copilot 线程不维护个人会话状态，其余会话推进本人阅读水位。
	if sendContext.Conversation.Type != string(domain.ConversationTypeCopilot) {
		if err := conversationaction.AdvanceConversationUserReadState(ctx, db, &servermodels.ConversationUserState{
			OrganizationID: identity.Organization.ID, ConversationID: input.ConversationID,
			UserID: identity.User.ID, LastReadMessageID: &message.ID,
		}, message); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	if sendContext.AgentIdentityID != "" {
		if err := scheduleAgentChatInput(ctx, db, identity.Organization.ID, sendContext, session, message.ID, agentScheduler); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	result := conversationaction.MemberConversationMessage(message, sendContext.SubjectID, identity.OrganizationIdentity)
	result.ReplyTo = replyTo
	return result, nil
}

type internalMessageContext struct {
	Conversation    *servermodels.Conversation `bun:"-"`
	ConversationID  string                     `bun:"conversation_id"`
	ParticipantID   string                     `bun:"participant_id"`
	SubjectID       string                     `bun:"subject_id"`
	AgentIdentityID string                     `bun:"agent_identity_id"`
	AgentRevisionID *string                    `bun:"agent_revision_id"`
	AgentPaused     bool                       `bun:"agent_paused"`
	AgentUnbound    bool                       `bun:"agent_unbound"`
	AgentActive     bool                       `bun:"agent_active"`
	ServiceOpen     bool                       `bun:"service_open"`
	AgentInputKind  domain.AgentInputKind      `bun:"-"`
}

// normalizeInternalMessageInput 规范化双方聊天正文和引用。
func normalizeInternalMessageInput(input InternalTextMessageInput) (InternalTextMessageInput, map[string]conversationaction.ValidationCode) {
	normalized, fields := conversationaction.NormalizeInternalTextMessageInput(conversationaction.InternalTextMessageFields{
		ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
	})
	input.ConversationID, input.ClientMessageID, input.Body, input.ReplyToMessageID = normalized.ConversationID, normalized.ClientMessageID, normalized.Body, normalized.ReplyToMessageID
	return input, fields
}
