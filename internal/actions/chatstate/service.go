//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// LockChannelConversation 锁定当前企业的渠道会话。
func LockChannelConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.Conversation, error) {
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Where("cv.workspace_id = ? AND cv.id = ? AND cv.type = ?", workspaceID, conversationID, domain.ConversationTypeChannel).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock channel conversation: %w", err)
	}
	return conversation, nil
}

// LockServiceConversation 锁定当前企业承载服务会话的会话。
func LockServiceConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.Conversation, error) {
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Where("cv.workspace_id = ? AND cv.id = ?", workspaceID, conversationID).
		Where("EXISTS (SELECT 1 FROM service_conversations AS svc WHERE svc.workspace_id = cv.workspace_id AND svc.conversation_id = cv.id)").
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock service conversation: %w", err)
	}
	return conversation, nil
}

// LoadServiceConversation 读取会话承载的服务会话。
func LoadServiceConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServiceConversation, error) {
	service := &servermodels.ServiceConversation{}
	err := db.NewSelect().Model(service).
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", workspaceID, conversationID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load service conversation: %w", err)
	}
	return service, nil
}

// LockCurrentServiceSession 在调用方持有会话锁后依次锁定服务会话和当前服务周期。
func LockCurrentServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServiceSession, error) {
	_, session, err := lockServiceAndCurrentSession(ctx, db, workspaceID, conversationID)
	return session, err
}

// lockServiceAndCurrentSession 在调用方持有会话锁后锁定服务会话行和当前服务周期，当前周期缺失时视为数据不一致。
func lockServiceAndCurrentSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServiceConversation, *servermodels.ServiceSession, error) {
	service, err := lockServiceConversationRow(ctx, db, workspaceID, conversationID)
	if err != nil {
		return nil, nil, err
	}
	if service.CurrentServiceSessionID == nil {
		return nil, nil, servicestate.ErrDataInvariant
	}
	session, err := lockCurrentSession(ctx, db, service)
	if err != nil {
		return nil, nil, err
	}
	return service, session, nil
}

// lockServiceConversationRow 锁定会话承载的服务会话行。
func lockServiceConversationRow(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServiceConversation, error) {
	service := &servermodels.ServiceConversation{}
	err := db.NewSelect().Model(service).
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", workspaceID, conversationID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, servicestate.ErrDataInvariant
	}
	if err != nil {
		return nil, fmt.Errorf("lock service conversation: %w", err)
	}
	return service, nil
}

// lockCurrentSession 在调用方已锁定服务会话行后锁定其当前服务周期，调用方保证当前周期存在。
func lockCurrentSession(ctx context.Context, db bun.IDB, service *servermodels.ServiceConversation) (*servermodels.ServiceSession, error) {
	session := &servermodels.ServiceSession{}
	err := db.NewSelect().Model(session).
		Where("ss.workspace_id = ? AND ss.service_conversation_id = ? AND ss.id = ?", service.WorkspaceID, service.ID, *service.CurrentServiceSessionID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, servicestate.ErrDataInvariant
	}
	if err != nil {
		return nil, fmt.Errorf("lock current service session: %w", err)
	}
	return session, nil
}

// LockedServiceSession 定义已依次锁定的会话、服务会话和当前服务周期。
type LockedServiceSession struct {
	Conversation *servermodels.Conversation
	Service      *servermodels.ServiceConversation
	Session      *servermodels.ServiceSession
}

// Source 返回服务会话的来源。
func (l LockedServiceSession) Source() domain.ServiceSource {
	return domain.ServiceSource(l.Service.Source)
}

// LockServiceSession 依次锁定承载服务会话的会话、服务会话和当前服务周期。
func LockServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (LockedServiceSession, error) {
	conversation, err := LockServiceConversation(ctx, db, workspaceID, conversationID)
	if err != nil {
		return LockedServiceSession{}, err
	}
	service, session, err := lockServiceAndCurrentSession(ctx, db, workspaceID, conversationID)
	if err != nil {
		return LockedServiceSession{}, err
	}
	return LockedServiceSession{Conversation: conversation, Service: service, Session: session}, nil
}

// LockServiceSessionByID 按周期编号依次锁定所属会话、服务会话和该服务周期，该周期可以是历史周期；周期不存在时返回 servicestate.ErrServiceSessionNotFound。
func LockServiceSessionByID(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID string) (LockedServiceSession, error) {
	var conversationID string
	err := db.NewSelect().Model((*servermodels.ServiceSession)(nil)).Column("conversation_id").
		Where("ss.workspace_id = ? AND ss.id = ?", workspaceID, serviceSessionID).
		Scan(ctx, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return LockedServiceSession{}, servicestate.ErrServiceSessionNotFound
	}
	if err != nil {
		return LockedServiceSession{}, fmt.Errorf("load service session conversation: %w", err)
	}
	conversation, err := LockConversation(ctx, db, workspaceID, conversationID)
	if err != nil {
		return LockedServiceSession{}, err
	}
	service, err := lockServiceConversationRow(ctx, db, workspaceID, conversationID)
	if err != nil {
		return LockedServiceSession{}, err
	}
	session := &servermodels.ServiceSession{}
	if err := db.NewSelect().Model(session).
		Where("ss.workspace_id = ? AND ss.service_conversation_id = ? AND ss.id = ?", workspaceID, service.ID, serviceSessionID).
		For("UPDATE").Scan(ctx); err != nil {
		return LockedServiceSession{}, fmt.Errorf("lock service session: %w", err)
	}
	return LockedServiceSession{Conversation: conversation, Service: service, Session: session}, nil
}

// SameTeam 判断两个所属队列是否相同，均为空表示同为公共队列。
func SameTeam(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// CreateServiceConversation 为会话建立服务会话。
func CreateServiceConversation(ctx context.Context, db bun.IDB, service *servermodels.ServiceConversation) error {
	if _, err := db.NewInsert().Model(service).
		Column("workspace_id", "conversation_id", "source", "requester_subject_id", "audience").
		Returning("*").
		Exec(ctx); err != nil {
		return fmt.Errorf("create service conversation: %w", err)
	}
	return nil
}

// LockOpenServiceSession 在调用方持有会话锁后锁定服务会话并返回进行中的当前服务周期，当前周期已结束或尚无周期时返回空。
func LockOpenServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServiceSession, error) {
	service, err := lockServiceConversationRow(ctx, db, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	if service.CurrentServiceSessionID == nil {
		return nil, nil
	}
	session, err := lockCurrentSession(ctx, db, service)
	if err != nil {
		return nil, err
	}
	switch domain.ServiceSessionStatus(session.Status) {
	case domain.ServiceSessionStatusOpen:
		return session, nil
	case domain.ServiceSessionStatusClosed:
		return nil, nil
	default:
		return nil, servicestate.ErrDataInvariant
	}
}

// OpenServiceSessionInput 定义开启服务周期的首条消息、开启时间、初始负责方、接待的 AI 员工与访客上下文；AgentIdentityID 为空表示周期由真人或队列首接待。
type OpenServiceSessionInput struct {
	ID                 string
	OpeningMessageID   string
	OpenedAt           time.Time
	TeamID             *string
	AssigneeIdentityID *string
	AgentIdentityID    *string
	VisitorContext     *domain.VisitorContext
}

// OpenServiceSession 在调用方持有会话锁且当前没有进行中周期时开启下一个服务周期，并设为服务会话的当前周期。
func OpenServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string, input OpenServiceSessionInput) (*servermodels.ServiceSession, error) {
	service, err := lockServiceConversationRow(ctx, db, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	var sequence int64
	if err := db.NewSelect().Model((*servermodels.ServiceSession)(nil)).
		ColumnExpr("COALESCE(MAX(ss.sequence), 0) + 1").
		Where("ss.workspace_id = ? AND ss.service_conversation_id = ?", workspaceID, service.ID).
		Scan(ctx, &sequence); err != nil {
		return nil, fmt.Errorf("load next service session sequence: %w", err)
	}
	session, err := servicestate.Open(ctx, db, servicestate.OpenInput{
		ID: input.ID, WorkspaceID: workspaceID, ConversationID: conversationID, ServiceConversationID: service.ID,
		Sequence: sequence, OpeningMessageID: input.OpeningMessageID, OpenedAt: input.OpenedAt,
		TeamID: input.TeamID, AssigneeIdentityID: input.AssigneeIdentityID, AgentIdentityID: input.AgentIdentityID,
		VisitorContext: input.VisitorContext,
	})
	if err != nil {
		return nil, err
	}
	if _, err := db.NewUpdate().Model(service).
		Set("current_service_session_id = ?", session.ID).
		WherePK().
		Where("workspace_id = ?", workspaceID).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("update current service session: %w", err)
	}
	return session, nil
}

// AppendRequesterStatus 在调用方持有会话锁的事务中为企业成员发起的服务会话写入发起人可见的服务进度事件并返回该事件；source 为服务会话来源，渠道来源不写入，返回空。
func AppendRequesterStatus(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource, status domain.ServiceRequestStatus, target *domain.ServiceSessionTarget, closeReason *domain.ServiceSessionCloseReason) (*servermodels.Message, error) {
	if source == domain.ServiceSourceChannel {
		return nil, nil
	}
	payload, err := json.Marshal(domain.ServiceStatusChangedEvent{ServiceSessionID: session.ID, Status: status, Target: target, CloseReason: closeReason})
	if err != nil {
		return nil, fmt.Errorf("encode service status event: %w", err)
	}
	eventType := string(domain.ConversationSystemEventServiceStatusChanged)
	message, _, err := AppendMessage(ctx, db, enqueuer, conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityRequester),
		SystemEventType: &eventType, SystemEventPayload: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("append service status event: %w", err)
	}
	return message, nil
}
