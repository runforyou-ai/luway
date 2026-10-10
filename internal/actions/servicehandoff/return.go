//go:build server

package servicehandoff

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentcancel"
	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Returner 在管理操作中把失去接待资格的身份负责的开放服务周期退回队列。
type Returner struct {
	enqueuer servertask.TxEnqueuer
}

// NewReturner 创建服务周期退回处理。
func NewReturner(enqueuer servertask.TxEnqueuer) *Returner {
	return &Returner{enqueuer: enqueuer}
}

// ReturnServiceSessionsToQueue 在管理操作事务中把失去接待资格的身份负责的开放服务周期退回队列：取消在途运行并结算输入队列，写入退回事件；原负责人是 AI 员工时投递转人工承接任务。
// audiences 限定退回的服务会话服务对象，为空表示全部。调用方已对该身份取 FOR UPDATE。
func (a *Returner) ReturnServiceSessionsToQueue(ctx context.Context, db bun.IDB, workspaceID, identityID, operationID string, audiences []domain.ServiceAudience) error {
	assignee := &servermodels.WorkspaceIdentity{}
	if err := db.NewSelect().Model(assignee).
		Column("oi.id", "oi.type", "oi.display_name").
		Where("oi.workspace_id = ? AND oi.id = ?", workspaceID, identityID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load unavailable service session assignee: %w", err)
	}
	var sessions []struct {
		ID             string `bun:"id"`
		ConversationID string `bun:"conversation_id"`
	}
	query := db.NewSelect().Model((*servermodels.ServiceSession)(nil)).
		Column("ss.id", "ss.conversation_id").
		Where("ss.workspace_id = ? AND ss.assignee_identity_id = ? AND ss.status = ?", workspaceID, identityID, domain.ServiceSessionStatusOpen)
	if len(audiences) > 0 {
		query = query.Join("JOIN service_conversations AS svc ON svc.workspace_id = ss.workspace_id AND svc.id = ss.service_conversation_id").
			Where("svc.audience IN (?)", bun.List(audiences))
	}
	if err := query.OrderExpr("ss.conversation_id").Scan(ctx, &sessions); err != nil {
		return fmt.Errorf("load assignee open service sessions: %w", err)
	}
	for _, row := range sessions {
		if _, err := ReturnUnavailableAssigneeSession(ctx, db, a.enqueuer, workspaceID, row.ConversationID, row.ID, assignee, "returned:"+row.ID+":"+operationID); err != nil {
			return err
		}
	}
	return nil
}

// ReturnUnavailableAssigneeSession 把失去接待资格的负责人所负责的指定周期退回队列并返回被取消的运行编号，周期已变化时跳过；调用方可以已在本事务中持有会话锁。
func ReturnUnavailableAssigneeSession(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, conversationID, serviceSessionID string, assignee *servermodels.WorkspaceIdentity, key string) ([]string, error) {
	locked, err := chatstate.LockServiceSession(ctx, db, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	session := locked.Session
	if session.ID != serviceSessionID || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen ||
		session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != assignee.ID {
		return nil, nil
	}
	var runIDs []string
	if domain.WorkspaceIdentityType(assignee.Type) == domain.WorkspaceIdentityTypeAgent {
		runIDs, err = agentcancel.CancelServiceSessionRuns(ctx, db, workspaceID, session.ID, assignee.ID, domain.AgentRunErrorCodeAgentUnavailable)
		if err != nil {
			return nil, err
		}
	}
	if err := applyServiceSessionReturn(ctx, db, enqueuer, locked, assignee, key); err != nil {
		return nil, err
	}
	return runIDs, nil
}

// applyServiceSessionReturn 在调用方持有会话锁的事务中写入退回事件并清空负责人，客户等待起点保持不变。
// 原负责人是 AI 员工时按转人工去向规则重新确定队列并投递转人工承接任务，由任务完成分配并按承接结果通知客户；原负责人是真人时保持原队列并投递重新分配任务。
func applyServiceSessionReturn(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, locked chatstate.LockedServiceSession, assignee *servermodels.WorkspaceIdentity, key string) error {
	conversation, session := locked.Conversation, locked.Session
	returnedByAgent := domain.WorkspaceIdentityType(assignee.Type) == domain.WorkspaceIdentityTypeAgent
	if returnedByAgent {
		// AI 员工交出的周期与主动转人工使用同一去向，已选择的咨询分类参与路由。
		resolved, err := resolveAgentHandoffQueue(ctx, db, session.WorkspaceID, session.ConversationID, assignee.ID, support.Deref(session.CategoryID))
		if err != nil {
			return err
		}
		session.TeamID = resolved.Queue.TeamID
	}
	target, err := serviceroute.ServiceSessionQueueTarget(ctx, db, session)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(domain.ServiceSessionReturnedEvent{
		ServiceSessionID: session.ID, FromIdentityID: assignee.ID, FromDisplayName: assignee.DisplayName,
		Target: target, Reason: domain.ServiceSessionReturnAssigneeUnavailable,
	})
	if err != nil {
		return fmt.Errorf("encode service session returned event: %w", err)
	}
	eventType := string(domain.ConversationSystemEventServiceSessionReturned)
	eventKey := key + ":event"
	if _, _, err := agentmessage.Append(ctx, db, enqueuer, conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
		SystemEventType: &eventType, SystemEventPayload: payload, IdempotencyKey: &eventKey,
	}); err != nil {
		return fmt.Errorf("append service session returned event: %w", err)
	}
	if _, err := chatstate.AppendRequesterStatus(ctx, db, enqueuer, conversation, session, locked.Source(), domain.ServiceRequestStatusHandedOff, &target, nil); err != nil {
		return err
	}
	// 写入时刻取持有会话锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return err
	}
	if err := servicestate.Begin(session).Queue(session.TeamID, now).Save(ctx, db, enqueuer); err != nil {
		return err
	}
	if returnedByAgent {
		if err := enqueueReturnedHandoff(ctx, db, enqueuer, ReturnedHandoffInput{
			WorkspaceID: session.WorkspaceID, ServiceSessionID: session.ID, AgentIdentityID: assignee.ID, NoticeKey: key,
		}); err != nil {
			return err
		}
	} else if err := serviceassignment.EnqueueAssign(ctx, db, enqueuer, serviceassignment.AssignInput{
		WorkspaceID: session.WorkspaceID, ServiceSessionID: session.ID,
	}); err != nil {
		return err
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户会话负责人失去接待资格，周期已退回队列",
		"conversation_id", session.ConversationID,
		"service_session_id", session.ID, "assignee_identity_id", assignee.ID,
		"target_kind", target.Kind)
	return nil
}
