//go:build server

package directchat

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// directServiceSession 在调用方持有 AI 聊天会话锁的事务中为发起人消息取得服务周期：进行中的周期继续承接；没有进行中的周期且 AI 员工服务员工时开启由该 AI 员工负责的新周期，首次开启时建立来源为单聊的服务会话；其余情况返回空，消息按普通 AI 聊天处理。
func directServiceSession(ctx context.Context, db bun.IDB, organizationID string, sendContext internalMessageContext, openingMessageID string, openedAt time.Time) (*servermodels.ServiceSession, error) {
	conversationID := sendContext.Conversation.ID
	service, err := chatstate.LoadServiceConversation(ctx, db, organizationID, conversationID)
	if errors.Is(err, chatstate.ErrConversationNotFound) {
		service = nil
	} else if err != nil {
		return nil, err
	}
	if service != nil {
		session, err := chatstate.LockOpenServiceSession(ctx, db, organizationID, conversationID)
		if err != nil || session != nil {
			return session, err
		}
	}
	serves, err := identityaction.ApplyDirectServiceAgentConditions(db.NewSelect().
		TableExpr("organization_identities AS oi").ColumnExpr("1").
		Where("oi.organization_id = ? AND oi.id = ?", organizationID, sendContext.AgentIdentityID), conversationID).
		Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("check direct service agent: %w", err)
	}
	if !serves {
		return nil, nil
	}
	if service == nil {
		if err := chatstate.CreateServiceConversation(ctx, db, &servermodels.ServiceConversation{
			OrganizationID: organizationID, ConversationID: conversationID, Source: string(domain.ServiceSourceDirect),
			RequesterSubjectID: sendContext.SubjectID, Audience: string(domain.ServiceAudienceEmployee),
		}); err != nil {
			return nil, err
		}
	}
	session, err := chatstate.OpenServiceSession(ctx, db, organizationID, conversationID, chatstate.OpenServiceSessionInput{
		ID: uuid.NewV7().String(), OpeningMessageID: openingMessageID, OpenedAt: openedAt, AssigneeIdentityID: &sendContext.AgentIdentityID,
		AgentIdentityID: &sendContext.AgentIdentityID,
	})
	if err != nil {
		return nil, err
	}
	// 新周期随开场消息的变更通知登记服务周期变化。
	if err := chatstate.NotifyConversationChanged(ctx, db, sendContext.Conversation, domain.ConversationChangeService); err != nil {
		return nil, err
	}
	return session, nil
}

// scheduleAgentChatInput 把 AI 聊天中的发起人消息交给 AI 员工：属于服务周期时追加到负责 AI 员工的服务输入流，周期由真人负责或在队列中时不调度；不属于服务周期时按普通 AI 聊天调度。
func scheduleAgentChatInput(ctx context.Context, db bun.IDB, organizationID string, sendContext internalMessageContext, session *servermodels.ServiceSession, messageID string, scheduler conversationaction.AgentChatMessageScheduler) error {
	if scheduler == nil {
		return conversationaction.ErrDataInvariant
	}
	if session != nil {
		if _, err := scheduler.ScheduleCustomerAuto(ctx, db, organizationID, sendContext.Conversation.ID, session.ID, messageID); err != nil {
			return fmt.Errorf("schedule service agent input: %w", err)
		}
		return nil
	}
	if sendContext.AgentRevisionID == nil {
		return conversationaction.ErrDataInvariant
	}
	if err := scheduler.Schedule(ctx, db, organizationID, sendContext.Conversation.ID, sendContext.AgentIdentityID, *sendContext.AgentRevisionID, messageID, sendContext.SubjectID, sendContext.AgentInputKind); err != nil {
		return fmt.Errorf("schedule agent input message: %w", err)
	}
	return nil
}
