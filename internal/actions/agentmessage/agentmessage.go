//go:build server

// Package agentmessage 以 AI 员工身份在会话中写入消息：结果消息与事件的幂等写入、客服会话中对客消息的外发与首响，以及客户会话中 AI 员工的参与者。
package agentmessage

import (
	"context"
	"fmt"
	"uuid"

	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Append 在 Run 终态门禁通过后追加结果消息，与运行终态共用事务；可能承载服务周期的会话同时登记服务周期变化；幂等重放时核对已有消息的类型与正文，不把另一类消息当作本次写入。
func Append(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, message *servermodels.Message) (*servermodels.Message, bool, error) {
	return appendMessage(ctx, db, enqueuer, conversation, nil, message)
}

// appendMessage 按 Append 的规则追加结果消息；session 为调用方已锁定的消息所属周期时同步其随消息推进的字段，可以为空。
func appendMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, message *servermodels.Message) (*servermodels.Message, bool, error) {
	appended, inserted, err := chatstate.AppendServiceMessage(ctx, db, enqueuer, conversation, session, message)
	if err != nil {
		return nil, false, err
	}
	if !inserted && (appended.Type != message.Type || appended.Body != message.Body) {
		return nil, false, fmt.Errorf("agent message idempotency key %q holds a different message", *message.IdempotencyKey)
	}
	// 终态运行派生业务查询与服务记录，客户会话与 AI 聊天随结果消息登记服务周期变化。
	if inserted && (conversation.Type == string(domain.ConversationTypeChannel) || conversation.Type == string(domain.ConversationTypeAgent)) {
		if err := chatstate.NotifyConversationChanged(ctx, db, conversation, domain.ConversationChangeService); err != nil {
			return nil, false, err
		}
	}
	return appended, inserted, nil
}

// AppendCustomer 在客服事务中追加 AI 客服消息：对客文本同事务按外发目标安排渠道投递并记录有效首响，内部消息只写入时间线。
func AppendCustomer(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, route deliveryaction.Route, message *servermodels.Message) (*servermodels.Message, error) {
	message, inserted, err := appendMessage(ctx, db, enqueuer, conversation, session, message)
	if err != nil || !inserted || message.Type != string(domain.MessageTypeText) || message.Visibility == string(domain.MessageVisibilityInternal) {
		return message, err
	}
	if route.Platform() {
		if err := deliveryaction.Enqueue(ctx, db, enqueuer, route, message); err != nil {
			return nil, err
		}
	}
	// 为推进周期摘要的对客文本记录首响。
	if err := servicestate.Begin(session).RecordAgentReply(message.ID, message.OriginatedAt).Save(ctx, db, enqueuer); err != nil {
		return nil, err
	}
	return message, nil
}

// EnsureCustomerParticipant 取得或创建客户会话中的 Agent 参与者。
func EnsureCustomerParticipant(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string) (string, error) {
	subject, err := chatstate.EnsureSubject(ctx, db, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, agentIdentityID, uuid.NewV7().String())
	if err != nil {
		return "", err
	}
	participant, err := chatstate.EnsureParticipant(ctx, db, workspaceID, conversationID, subject.ID, uuid.NewV7().String())
	if err != nil {
		return "", err
	}
	return participant.ID, nil
}
