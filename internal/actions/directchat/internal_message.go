//go:build server

package directchat

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/membersend"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/uptrace/bun"
)

// internalMessage 是内部会话一次发送的内容，Attachment 不为空时发送附件消息。
type internalMessage struct {
	ConversationID   string
	ClientMessageID  string
	Body             string
	ReplyToMessageID string
	Attachment       *membersend.AttachmentIntent
}

// intent 返回该次发送对应的幂等核对意图。
func (m internalMessage) intent() membersend.Intent {
	if m.Attachment != nil {
		return membersend.InternalAttachment(m.ConversationID, m.Body, *m.Attachment)
	}
	return membersend.InternalText(m.ConversationID, m.Body, m.ReplyToMessageID)
}

// replayInternalMessage 在校验发送资格前按发送编号读取本人已保存的消息，发送资格失效后重放仍返回原消息。
func replayInternalMessage(ctx context.Context, db bun.IDB, identity *servermodels.Identity, input internalMessage) (conversationaction.ConversationMessage, bool, error) {
	return membersend.Replay(ctx, db, identity, input.intent(), membersend.Key(identity, input.ClientMessageID))
}

// saveInternalMessage 在会话与成员已锁定的事务内幂等保存文本或附件消息、个人状态和 Agent 输入，AI 聊天中的消息按 AI 员工的服务对象进入服务周期。
func saveInternalMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, identity *servermodels.Identity, input internalMessage, sendContext internalMessageContext, agentScheduler conversationaction.AgentChatMessageScheduler) (conversationaction.ConversationMessage, error) {
	// 持有会话锁后复核幂等结果，再写入文件激活、服务周期等关联记录。
	if saved, found, err := replayInternalMessage(ctx, db, identity, input); err != nil || found {
		return saved, err
	}
	replyTo, err := conversationaction.LoadConversationReplyTarget(ctx, db, identity.Workspace.ID, input.ConversationID, input.ReplyToMessageID)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	// 只能引用会话各方可见的消息。
	if replyTo != nil && replyTo.Visibility != domain.MessageVisibilityShared {
		return conversationaction.ConversationMessage{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonReplyTargetInvalid}
	}
	// 消息时刻取持有会话锁之后的数据库时刻。
	originatedAt, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	draft := membersend.Draft{
		ConversationID: input.ConversationID, ParticipantID: sendContext.ParticipantID, ClientMessageID: input.ClientMessageID,
		Type: domain.MessageTypeText, Body: input.Body, OriginatedAt: originatedAt,
	}
	var attachment *conversationaction.MessageAttachment
	if input.Attachment != nil {
		if attachment, err = membersend.ActivateAttachment(ctx, db, identity, *input.Attachment); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
		draft.Type = domain.MessageTypeAttachment
	}
	message := membersend.NewMessage(identity, draft)
	if replyTo != nil {
		message.ReplyToMessageID = &replyTo.ID
	}
	if attachment != nil {
		message.SearchVector = searchtext.Vector(input.Body, attachment.Name)
	}
	session, err := assignAgentDirectSession(ctx, db, identity, sendContext, message)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	message, inserted, err := chatstate.AppendMessage(ctx, db, enqueuer, sendContext.Conversation, message)
	if err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	if !inserted {
		saved, _, err := replayInternalMessage(ctx, db, identity, input)
		return saved, err
	}
	if attachment != nil {
		if err := conversationaction.SaveMessageAttachment(ctx, db, identity.Workspace.ID, message.ID, *attachment); err != nil {
			return conversationaction.ConversationMessage{}, err
		}
	}
	if err := afterInternalMessage(ctx, db, identity, sendContext, session, message, agentScheduler); err != nil {
		return conversationaction.ConversationMessage{}, err
	}
	result := conversationaction.MemberConversationMessage(message, sendContext.SubjectID, identity.WorkspaceIdentity)
	result.ReplyTo = replyTo
	result.Attachment = attachment
	return result, nil
}

// assignAgentDirectSession 把 AI 聊天中的发起人消息按 AI 员工的服务对象分配进服务周期，其他会话返回 nil。
func assignAgentDirectSession(ctx context.Context, db bun.IDB, identity *servermodels.Identity, sendContext internalMessageContext, message *servermodels.Message) (*servermodels.ServiceSession, error) {
	if sendContext.AgentInputKind != domain.AgentInputKindAgentDirect {
		return nil, nil
	}
	session, err := directServiceSession(ctx, db, identity.Workspace.ID, sendContext, message.ID, message.OriginatedAt)
	if err != nil || session == nil {
		return nil, err
	}
	message.ServiceSessionID = &session.ID
	return session, nil
}

// afterInternalMessage 在新消息写入后推进本人阅读水位，并为 AI 聊天与 Copilot 线程追加 Agent 输入。
func afterInternalMessage(ctx context.Context, db bun.IDB, identity *servermodels.Identity, sendContext internalMessageContext, session *servermodels.ServiceSession, message *servermodels.Message, agentScheduler conversationaction.AgentChatMessageScheduler) error {
	// Copilot 线程不维护个人会话状态，其余会话推进本人阅读水位。
	if sendContext.Conversation.Type != string(domain.ConversationTypeCopilot) {
		if err := membersend.MarkSenderRead(ctx, db, identity, message); err != nil {
			return err
		}
	}
	if sendContext.AgentIdentityID == "" {
		return nil
	}
	return scheduleAgentChatInput(ctx, db, identity.Workspace.ID, sendContext, session, message.ID, agentScheduler)
}

// internalMessageContext 是内部会话发送时锁定的会话、发送者参与者与 AI 员工发送资格。
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
func normalizeInternalMessageInput(input InternalTextMessageInput) InternalTextMessageInput {
	normalized := conversationaction.NormalizeInternalTextMessageInput(conversationaction.InternalTextMessageFields{
		ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: input.ReplyToMessageID,
	})
	input.ConversationID, input.ClientMessageID, input.Body, input.ReplyToMessageID = normalized.ConversationID, normalized.ClientMessageID, normalized.Body, normalized.ReplyToMessageID
	return input
}
