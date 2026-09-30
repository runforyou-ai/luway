//go:build server

package servicesession

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// appendServiceSessionEvent 在调用方持有会话锁的事务中写入成员操作服务周期的系统事件，仅成员可见，不改变周期摘要与首响，并按服务会话来源 source 为企业成员发起人写入对应的服务进度；fromIdentityID 为原负责人。
func appendServiceSessionEvent(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource,
	eventType domain.ConversationSystemEventType, fromIdentityID *string, target *domain.ServiceSessionTarget) error {
	event := domain.ServiceSessionOperatedEvent{
		ServiceSessionID: session.ID, ActorIdentityID: identity.OrganizationIdentity.ID, ActorDisplayName: identity.OrganizationIdentity.DisplayName,
		FromIdentityID: fromIdentityID, Target: target,
	}
	if fromIdentityID != nil {
		var fromName string
		if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).Column("display_name").
			Where("oi.organization_id = ? AND oi.id = ?", session.OrganizationID, *fromIdentityID).
			Scan(ctx, &fromName); err != nil {
			return fmt.Errorf("load service session previous assignee: %w", err)
		}
		event.FromDisplayName = &fromName
	}
	if err := appendServiceSessionOperatedEvent(ctx, db, conversation, session, eventType, event); err != nil {
		return err
	}
	// 发起人看到的进度：成员领取、接管或重开时为该成员处理中，转给成员或交还 AI 员工时为其处理中，转回队列时为已转交。
	switch {
	case eventType != domain.ConversationSystemEventServiceSessionTransferred:
		actor := domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: &identity.OrganizationIdentity.ID, DisplayName: &identity.OrganizationIdentity.DisplayName}
		_, err := chatstate.AppendRequesterStatus(ctx, db, conversation, session, source, domain.ServiceRequestStatusProcessing, &actor, nil)
		return err
	case target.Kind == domain.ServiceSessionTargetMember:
		_, err := chatstate.AppendRequesterStatus(ctx, db, conversation, session, source, domain.ServiceRequestStatusProcessing, target, nil)
		return err
	default:
		_, err := chatstate.AppendRequesterStatus(ctx, db, conversation, session, source, domain.ServiceRequestStatusHandedOff, target, nil)
		return err
	}
}

// appendServiceSessionClosedEvent 在调用方持有会话锁的事务中写入服务周期关闭事件，记录关闭人与结束方式，仅成员可见，并为企业成员发起人写入服务结束进度。
func appendServiceSessionClosedEvent(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource,
	actorIdentityID, actorDisplayName string, reason domain.ServiceSessionCloseReason) error {
	if err := appendServiceSessionOperatedEvent(ctx, db, conversation, session, domain.ConversationSystemEventServiceSessionClosed, domain.ServiceSessionOperatedEvent{
		ServiceSessionID: session.ID, ActorIdentityID: actorIdentityID, ActorDisplayName: actorDisplayName, CloseReason: &reason,
	}); err != nil {
		return err
	}
	_, err := chatstate.AppendRequesterStatus(ctx, db, conversation, session, source, domain.ServiceRequestStatusClosed, nil, &reason)
	return err
}

// appendServiceSessionOperatedEvent 把服务周期操作事件写入会话时间线，仅成员可见。
func appendServiceSessionOperatedEvent(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, session *servermodels.ServiceSession,
	eventType domain.ConversationSystemEventType, event domain.ServiceSessionOperatedEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode service session event: %w", err)
	}
	typeName := string(eventType)
	if _, _, err := chatstate.AppendMessage(ctx, db, conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: session.OrganizationID, ConversationID: session.ConversationID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
		SystemEventType: &typeName, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append service session event: %w", err)
	}
	return nil
}
